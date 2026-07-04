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

const StatusPending = "pending"

var ErrStorePathRequired = errors.New("store path is required")

type Request struct {
	ID            string    `json:"id"`
	DiscordUserID string    `json:"discord_user_id"`
	DiscordName   string    `json:"discord_name,omitempty"`
	GuildID       string    `json:"guild_id,omitempty"`
	ChannelID     string    `json:"channel_id,omitempty"`
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type RequestInput struct {
	DiscordUserID string
	DiscordName   string
	GuildID       string
	ChannelID     string
}

type Store interface {
	CreateOrGetPending(input RequestInput) (Request, bool, error)
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
