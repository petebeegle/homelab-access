package access

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

const (
	StatusPending  = "pending"
	StatusApproved = "approved"
	StatusDenied   = "denied"
)

var (
	ErrRequestNotFound   = errors.New("request not found")
	ErrRequestNotPending = errors.New("request is not pending")
	ErrDownloadNotFound  = errors.New("download token not found")
	ErrDownloadExpired   = errors.New("download token expired")
	ErrDownloadConsumed  = errors.New("download token already consumed")
	ErrStorePathRequired = errors.New("store path is required")
)

type Request struct {
	ID                     string    `json:"id"`
	DiscordUserID          string    `json:"discord_user_id"`
	DiscordName            string    `json:"discord_name,omitempty"`
	GuildID                string    `json:"guild_id,omitempty"`
	ChannelID              string    `json:"channel_id,omitempty"`
	Status                 string    `json:"status"`
	ReviewedBy             string    `json:"reviewed_by,omitempty"`
	ReviewedAt             time.Time `json:"reviewed_at,omitempty"`
	AuthentikUserID        string    `json:"authentik_user_id,omitempty"`
	AuthentikUsername      string    `json:"authentik_username,omitempty"`
	WireGuardClientID      string    `json:"wireguard_client_id,omitempty"`
	WireGuardConfiguration string    `json:"wireguard_configuration,omitempty"`
	DownloadToken          string    `json:"download_token,omitempty"`
	DownloadTokenExpiresAt time.Time `json:"download_token_expires_at,omitempty"`
	DownloadConsumedAt     time.Time `json:"download_consumed_at,omitempty"`
	CreatedAt              time.Time `json:"created_at"`
	UpdatedAt              time.Time `json:"updated_at"`
}

type RequestInput struct {
	DiscordUserID string
	DiscordName   string
	GuildID       string
	ChannelID     string
}

type Store interface {
	CreateOrGetPending(input RequestInput) (Request, bool, error)
	GetPending(id string) (Request, error)
	Approve(id string, input ApprovalInput) (Request, error)
	Deny(id, reviewerID string) (Request, error)
	GetDownload(token string) (Request, error)
	ConsumeDownload(token string) (Request, error)
}

type ApprovalInput struct {
	ReviewerID             string
	AuthentikUserID        string
	AuthentikUsername      string
	WireGuardClientID      string
	WireGuardConfiguration string
	DownloadToken          string
	DownloadTokenExpiresAt time.Time
}

type FileStore struct {
	path string
	now  func() time.Time

	mu   sync.Mutex
	data storeData
}

type storeData struct {
	Requests []Request `json:"requests"`
}

func OpenFileStore(path string) (*FileStore, error) {
	if path == "" {
		return nil, ErrStorePathRequired
	}

	store := &FileStore{
		path: path,
		now:  func() time.Time { return time.Now().UTC() },
	}

	if err := store.load(); err != nil {
		return nil, err
	}

	return store, nil
}

func (s *FileStore) CreateOrGetPending(input RequestInput) (Request, bool, error) {
	if input.DiscordUserID == "" {
		return Request{}, false, errors.New("discord user id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, request := range s.data.Requests {
		if request.DiscordUserID == input.DiscordUserID && request.Status == StatusPending {
			return request, false, nil
		}
	}

	now := s.now()
	request := Request{
		ID:            "req_" + strconv.FormatInt(now.UnixNano(), 36),
		DiscordUserID: input.DiscordUserID,
		DiscordName:   input.DiscordName,
		GuildID:       input.GuildID,
		ChannelID:     input.ChannelID,
		Status:        StatusPending,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	s.data.Requests = append(s.data.Requests, request)
	if err := s.saveLocked(); err != nil {
		s.data.Requests = s.data.Requests[:len(s.data.Requests)-1]
		return Request{}, false, err
	}

	return request, true, nil
}

func (s *FileStore) GetPending(id string) (Request, error) {
	if id == "" {
		return Request{}, ErrRequestNotFound
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, request := range s.data.Requests {
		if request.ID != id {
			continue
		}
		if request.Status != StatusPending {
			return Request{}, ErrRequestNotPending
		}
		return request, nil
	}
	return Request{}, ErrRequestNotFound
}

func (s *FileStore) Approve(id string, input ApprovalInput) (Request, error) {
	if id == "" {
		return Request{}, ErrRequestNotFound
	}
	if input.ReviewerID == "" {
		return Request{}, errors.New("reviewer id is required")
	}
	if input.WireGuardConfiguration == "" {
		return Request{}, errors.New("wireguard configuration is required")
	}
	if input.DownloadToken == "" {
		return Request{}, errors.New("download token is required")
	}
	if input.DownloadTokenExpiresAt.IsZero() {
		return Request{}, errors.New("download token expiry is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.data.Requests {
		if s.data.Requests[i].ID != id {
			continue
		}
		if s.data.Requests[i].Status != StatusPending {
			return Request{}, ErrRequestNotPending
		}

		previous := s.data.Requests[i]
		now := s.now()
		s.data.Requests[i].Status = StatusApproved
		s.data.Requests[i].ReviewedBy = input.ReviewerID
		s.data.Requests[i].ReviewedAt = now
		s.data.Requests[i].AuthentikUserID = input.AuthentikUserID
		s.data.Requests[i].AuthentikUsername = input.AuthentikUsername
		s.data.Requests[i].WireGuardClientID = input.WireGuardClientID
		s.data.Requests[i].WireGuardConfiguration = input.WireGuardConfiguration
		s.data.Requests[i].DownloadToken = input.DownloadToken
		s.data.Requests[i].DownloadTokenExpiresAt = input.DownloadTokenExpiresAt
		s.data.Requests[i].UpdatedAt = now
		if err := s.saveLocked(); err != nil {
			s.data.Requests[i] = previous
			return Request{}, err
		}
		return s.data.Requests[i], nil
	}

	return Request{}, ErrRequestNotFound
}

func (s *FileStore) Deny(id, reviewerID string) (Request, error) {
	return s.review(id, reviewerID, StatusDenied)
}

func (s *FileStore) GetDownload(token string) (Request, error) {
	if token == "" {
		return Request{}, ErrDownloadNotFound
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.getDownloadLocked(token, false)
}

func (s *FileStore) ConsumeDownload(token string) (Request, error) {
	if token == "" {
		return Request{}, ErrDownloadNotFound
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.getDownloadLocked(token, true)
}

func (s *FileStore) getDownloadLocked(token string, consume bool) (Request, error) {
	for i := range s.data.Requests {
		if s.data.Requests[i].DownloadToken != token {
			continue
		}
		if s.data.Requests[i].Status != StatusApproved || s.data.Requests[i].WireGuardConfiguration == "" {
			return Request{}, ErrDownloadNotFound
		}
		if !s.data.Requests[i].DownloadConsumedAt.IsZero() {
			return Request{}, ErrDownloadConsumed
		}
		now := s.now()
		if !s.data.Requests[i].DownloadTokenExpiresAt.IsZero() && now.After(s.data.Requests[i].DownloadTokenExpiresAt) {
			return Request{}, ErrDownloadExpired
		}
		if !consume {
			return s.data.Requests[i], nil
		}

		previous := s.data.Requests[i]
		s.data.Requests[i].DownloadConsumedAt = now
		s.data.Requests[i].UpdatedAt = now
		if err := s.saveLocked(); err != nil {
			s.data.Requests[i] = previous
			return Request{}, err
		}
		return s.data.Requests[i], nil
	}

	return Request{}, ErrDownloadNotFound
}

func (s *FileStore) review(id, reviewerID, status string) (Request, error) {
	if id == "" {
		return Request{}, ErrRequestNotFound
	}
	if reviewerID == "" {
		return Request{}, errors.New("reviewer id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.data.Requests {
		if s.data.Requests[i].ID != id {
			continue
		}
		if s.data.Requests[i].Status != StatusPending {
			return Request{}, ErrRequestNotPending
		}

		previous := s.data.Requests[i]
		now := s.now()
		s.data.Requests[i].Status = status
		s.data.Requests[i].ReviewedBy = reviewerID
		s.data.Requests[i].ReviewedAt = now
		s.data.Requests[i].UpdatedAt = now
		if err := s.saveLocked(); err != nil {
			s.data.Requests[i] = previous
			return Request{}, err
		}
		return s.data.Requests[i], nil
	}

	return Request{}, ErrRequestNotFound
}

func (s *FileStore) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	content, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			s.data = storeData{Requests: []Request{}}
			return nil
		}
		return fmt.Errorf("read store: %w", err)
	}
	if len(content) == 0 {
		s.data = storeData{Requests: []Request{}}
		return nil
	}
	if err := json.Unmarshal(content, &s.data); err != nil {
		return fmt.Errorf("decode store: %w", err)
	}
	if s.data.Requests == nil {
		s.data.Requests = []Request{}
	}
	return nil
}

func (s *FileStore) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create store directory: %w", err)
	}

	content, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return fmt.Errorf("encode store: %w", err)
	}
	content = append(content, '\n')

	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".homelab-access-*.json")
	if err != nil {
		return fmt.Errorf("create temporary store: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		_ = os.Remove(tmpName)
	}()

	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temporary store: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temporary store: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary store: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("replace store: %w", err)
	}

	return nil
}
