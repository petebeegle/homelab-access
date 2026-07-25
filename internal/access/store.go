package access

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"
)

var (
	ErrRequestNotFound        = errors.New("request not found")
	ErrRequestNotPending      = errors.New("request is not pending")
	ErrRequestNotProvisioning = errors.New("request is not provisioning")
	ErrApprovalInProgress     = fmt.Errorf("%w: approval is in progress", ErrRequestNotPending)
	ErrApprovalClaimMismatch  = errors.New("approval claim does not match")
	ErrActiveGrantExists      = errors.New("principal already has an active grant")
	ErrPrincipalNotFound      = errors.New("principal not found")
	ErrGrantNotFound          = errors.New("active grant not found")
	ErrDownloadNotFound       = errors.New("download token not found")
	ErrDownloadExpired        = errors.New("download token expired")
	ErrDownloadConsumed       = errors.New("download token already consumed")
	ErrStorePathRequired      = errors.New("store path is required")
)

// The file store has no durable worker lease yet, so stale claims become
// retryable after the longest provisioning attempt has timed out.
const approvalClaimTTL = 3 * time.Minute

type Request struct {
	ID                     string        `json:"id"`
	PrincipalID            PrincipalID   `json:"principal_id,omitempty"`
	DiscordUserID          string        `json:"discord_user_id"`
	DiscordName            string        `json:"discord_name,omitempty"`
	GuildID                string        `json:"guild_id,omitempty"`
	ChannelID              string        `json:"channel_id,omitempty"`
	Status                 RequestStatus `json:"status"`
	ApprovalClaimID        string        `json:"approval_claim_id,omitempty"`
	ClaimedBy              string        `json:"claimed_by,omitempty"`
	ClaimedAt              time.Time     `json:"claimed_at,omitempty"`
	ReviewedBy             string        `json:"reviewed_by,omitempty"`
	ReviewedAt             time.Time     `json:"reviewed_at,omitempty"`
	AuthentikUserID        string        `json:"authentik_user_id,omitempty"`
	AuthentikUsername      string        `json:"authentik_username,omitempty"`
	WireGuardClientID      string        `json:"wireguard_client_id,omitempty"`
	WireGuardConfiguration string        `json:"wireguard_configuration,omitempty"`
	DownloadToken          string        `json:"download_token,omitempty"`
	DownloadTokenExpiresAt time.Time     `json:"download_token_expires_at,omitempty"`
	DownloadConsumedAt     time.Time     `json:"download_consumed_at,omitempty"`
	CreatedAt              time.Time     `json:"created_at"`
	UpdatedAt              time.Time     `json:"updated_at"`
}

type RequestInput struct {
	DiscordUserID string
	DiscordName   string
	GuildID       string
	ChannelID     string
}

type ApprovalInput struct {
	ReviewerID             string
	AuthentikUserID        string
	AuthentikUsername      string
	IdentityBrokerOwned    bool
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

var _ Store = (*FileStore)(nil)

type storeData struct {
	Requests   []Request   `json:"requests"`
	Principals []Principal `json:"principals,omitempty"`
	Grants     []Grant     `json:"grants,omitempty"`
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
	unlock, err := s.lockAndRefreshLocked()
	if err != nil {
		return Request{}, false, err
	}
	defer unlock()

	principalID := PrincipalID(input.DiscordUserID)
	if _, ok := s.activeGrantLocked(principalID); ok {
		return Request{}, false, ErrActiveGrantExists
	}

	for i := range s.data.Requests {
		request := &s.data.Requests[i]
		if requestPrincipalID(*request) != principalID {
			continue
		}
		switch request.Status {
		case StatusPending, StatusClaimed, StatusProvisioning:
			if input.DiscordName != "" && request.DiscordName != input.DiscordName {
				previous := s.snapshotLocked()
				now := s.now()
				request.DiscordName = input.DiscordName
				request.UpdatedAt = now
				s.upsertPrincipalLocked(principalID, input.DiscordName, now)
				if err := s.saveLocked(); err != nil {
					s.data = previous
					return Request{}, false, err
				}
			}
			return *request, false, nil
		}
	}

	now := s.now()
	request := Request{
		ID:            "req_" + strconv.FormatInt(now.UnixNano(), 36),
		PrincipalID:   principalID,
		DiscordUserID: input.DiscordUserID,
		DiscordName:   input.DiscordName,
		GuildID:       input.GuildID,
		ChannelID:     input.ChannelID,
		Status:        StatusPending,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	previous := s.snapshotLocked()
	s.upsertPrincipalLocked(principalID, input.DiscordName, now)
	s.data.Requests = append(s.data.Requests, request)
	if err := s.saveLocked(); err != nil {
		s.data = previous
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
	unlock, err := s.lockAndRefreshLocked()
	if err != nil {
		return Request{}, err
	}
	defer unlock()

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
	if err := validateApprovalInput(input); err != nil {
		return Request{}, err
	}

	claim, err := s.ClaimApproval(id, input.ReviewerID)
	if err != nil {
		return Request{}, err
	}
	if _, err := s.BeginApprovalProvisioning(claim); err != nil {
		_ = s.ReleaseApprovalClaim(id, claim.ID)
		return Request{}, err
	}
	request, err := s.FinalizeApproval(claim, input)
	if err != nil {
		_ = s.ReleaseApprovalClaim(id, claim.ID)
		return Request{}, err
	}
	return request, nil
}

func (s *FileStore) ClaimApproval(id, reviewerID string) (ApprovalClaim, error) {
	if id == "" {
		return ApprovalClaim{}, ErrRequestNotFound
	}
	if reviewerID == "" {
		return ApprovalClaim{}, errors.New("reviewer id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := s.lockAndRefreshLocked()
	if err != nil {
		return ApprovalClaim{}, err
	}
	defer unlock()

	for i := range s.data.Requests {
		if s.data.Requests[i].ID != id {
			continue
		}
		switch s.data.Requests[i].Status {
		case StatusClaimed, StatusProvisioning:
			return ApprovalClaim{}, ErrApprovalInProgress
		case StatusPending:
		default:
			return ApprovalClaim{}, ErrRequestNotPending
		}

		principalID := requestPrincipalID(s.data.Requests[i])
		if _, ok := s.activeGrantLocked(principalID); ok {
			return ApprovalClaim{}, ErrActiveGrantExists
		}
		for j := range s.data.Requests {
			if i == j || requestPrincipalID(s.data.Requests[j]) != principalID {
				continue
			}
			if s.data.Requests[j].Status == StatusClaimed || s.data.Requests[j].Status == StatusProvisioning {
				return ApprovalClaim{}, ErrApprovalInProgress
			}
		}

		previous := s.snapshotLocked()
		now := s.now()
		claimID := "claim_" + id + "_" + strconv.FormatInt(now.UnixNano(), 36)
		s.data.Requests[i].Status = StatusClaimed
		s.data.Requests[i].ApprovalClaimID = claimID
		s.data.Requests[i].ClaimedBy = reviewerID
		s.data.Requests[i].ClaimedAt = now
		s.data.Requests[i].UpdatedAt = now
		if err := s.saveLocked(); err != nil {
			s.data = previous
			return ApprovalClaim{}, err
		}
		return ApprovalClaim{
			ID:         claimID,
			Request:    s.data.Requests[i],
			ReviewerID: reviewerID,
			ClaimedAt:  now,
		}, nil
	}

	return ApprovalClaim{}, ErrRequestNotFound
}

func (s *FileStore) BeginApprovalProvisioning(claim ApprovalClaim) (Request, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := s.lockAndRefreshLocked()
	if err != nil {
		return Request{}, err
	}
	defer unlock()

	for i := range s.data.Requests {
		if s.data.Requests[i].ID != claim.Request.ID {
			continue
		}
		if s.data.Requests[i].Status != StatusClaimed || !claimMatches(s.data.Requests[i], claim) {
			return Request{}, ErrApprovalClaimMismatch
		}

		previous := s.snapshotLocked()
		s.data.Requests[i].Status = StatusProvisioning
		s.data.Requests[i].UpdatedAt = s.now()
		if err := s.saveLocked(); err != nil {
			s.data = previous
			return Request{}, err
		}
		return s.data.Requests[i], nil
	}
	return Request{}, ErrRequestNotFound
}

func (s *FileStore) ReleaseApprovalClaim(requestID, claimID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := s.lockAndRefreshLocked()
	if err != nil {
		return err
	}
	defer unlock()

	for i := range s.data.Requests {
		if s.data.Requests[i].ID != requestID {
			continue
		}
		if (s.data.Requests[i].Status != StatusClaimed && s.data.Requests[i].Status != StatusProvisioning) ||
			s.data.Requests[i].ApprovalClaimID != claimID {
			return ErrApprovalClaimMismatch
		}

		previous := s.snapshotLocked()
		now := s.now()
		s.data.Requests[i].Status = StatusPending
		s.data.Requests[i].ApprovalClaimID = ""
		s.data.Requests[i].ClaimedBy = ""
		s.data.Requests[i].ClaimedAt = time.Time{}
		s.data.Requests[i].UpdatedAt = now
		if err := s.saveLocked(); err != nil {
			s.data = previous
			return err
		}
		return nil
	}
	return ErrRequestNotFound
}

func (s *FileStore) FailApproval(claim ApprovalClaim) (Request, error) {
	if claim.ID == "" || claim.Request.ID == "" {
		return Request{}, ErrApprovalClaimMismatch
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := s.lockAndRefreshLocked()
	if err != nil {
		return Request{}, err
	}
	defer unlock()

	for i := range s.data.Requests {
		if s.data.Requests[i].ID != claim.Request.ID {
			continue
		}
		if s.data.Requests[i].Status != StatusProvisioning ||
			!claimMatches(s.data.Requests[i], claim) {
			return Request{}, ErrApprovalClaimMismatch
		}

		previous := s.snapshotLocked()
		now := s.now()
		s.data.Requests[i].Status = StatusFailed
		s.data.Requests[i].ApprovalClaimID = ""
		s.data.Requests[i].UpdatedAt = now
		if err := s.saveLocked(); err != nil {
			s.data = previous
			return Request{}, err
		}
		return s.data.Requests[i], nil
	}
	return Request{}, ErrRequestNotFound
}

func (s *FileStore) FinalizeApproval(claim ApprovalClaim, input ApprovalInput) (Request, error) {
	if err := validateApprovalInput(input); err != nil {
		return Request{}, err
	}
	if claim.ID == "" || claim.Request.ID == "" {
		return Request{}, ErrApprovalClaimMismatch
	}
	if input.ReviewerID != claim.ReviewerID {
		return Request{}, ErrApprovalClaimMismatch
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := s.lockAndRefreshLocked()
	if err != nil {
		return Request{}, err
	}
	defer unlock()

	for i := range s.data.Requests {
		if s.data.Requests[i].ID != claim.Request.ID {
			continue
		}
		if !claimMatches(s.data.Requests[i], claim) {
			return Request{}, ErrApprovalClaimMismatch
		}
		if s.data.Requests[i].Status != StatusProvisioning {
			return Request{}, ErrRequestNotProvisioning
		}

		principalID := requestPrincipalID(s.data.Requests[i])
		if _, ok := s.activeGrantLocked(principalID); ok {
			return Request{}, ErrActiveGrantExists
		}

		previous := s.snapshotLocked()
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
		s.data.Grants = append(s.data.Grants, Grant{
			ID:                  "grant_" + s.data.Requests[i].ID,
			PrincipalID:         principalID,
			RequestID:           s.data.Requests[i].ID,
			State:               GrantStateActive,
			AuthentikUserID:     input.AuthentikUserID,
			WireGuardClientID:   input.WireGuardClientID,
			IdentityBrokerOwned: input.IdentityBrokerOwned,
			WireGuardManaged:    true,
			StartsAt:            now,
			CreatedAt:           now,
			UpdatedAt:           now,
		})
		if err := s.saveLocked(); err != nil {
			s.data = previous
			return Request{}, err
		}
		return s.data.Requests[i], nil
	}

	return Request{}, ErrRequestNotFound
}

func (s *FileStore) Deny(id, reviewerID string) (Request, error) {
	return s.review(id, reviewerID, StatusDenied)
}

func (s *FileStore) GetPrincipal(id PrincipalID) (Principal, error) {
	if id == "" {
		return Principal{}, ErrPrincipalNotFound
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := s.lockAndRefreshLocked()
	if err != nil {
		return Principal{}, err
	}
	defer unlock()

	for _, principal := range s.data.Principals {
		if principal.ID == id {
			return principal, nil
		}
	}
	return Principal{}, ErrPrincipalNotFound
}

func (s *FileStore) GetActiveGrant(principalID PrincipalID) (Grant, error) {
	if principalID == "" {
		return Grant{}, ErrGrantNotFound
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := s.lockAndRefreshLocked()
	if err != nil {
		return Grant{}, err
	}
	defer unlock()

	grant, ok := s.activeGrantLocked(principalID)
	if !ok {
		return Grant{}, ErrGrantNotFound
	}
	return grant, nil
}

func (s *FileStore) GetDownload(token string) (Request, error) {
	if token == "" {
		return Request{}, ErrDownloadNotFound
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := s.lockAndRefreshLocked()
	if err != nil {
		return Request{}, err
	}
	defer unlock()

	return s.getDownloadLocked(token, false)
}

func (s *FileStore) ConsumeDownload(token string) (Request, error) {
	if token == "" {
		return Request{}, ErrDownloadNotFound
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := s.lockAndRefreshLocked()
	if err != nil {
		return Request{}, err
	}
	defer unlock()

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

func (s *FileStore) review(id, reviewerID string, status RequestStatus) (Request, error) {
	if id == "" {
		return Request{}, ErrRequestNotFound
	}
	if reviewerID == "" {
		return Request{}, errors.New("reviewer id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := s.lockAndRefreshLocked()
	if err != nil {
		return Request{}, err
	}
	defer unlock()

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

	return s.loadLocked()
}

func (s *FileStore) loadLocked() error {
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
	if s.data.Principals == nil {
		s.data.Principals = []Principal{}
	}
	if s.data.Grants == nil {
		s.data.Grants = []Grant{}
	}
	s.normalizeLegacyDataLocked()
	return nil
}

func (s *FileStore) lockAndRefreshLocked() (func(), error) {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return nil, fmt.Errorf("create store directory: %w", err)
	}
	lock, err := os.OpenFile(s.path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open store lock: %w", err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("lock store: %w", err)
	}
	unlock := func() {
		_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		_ = lock.Close()
	}
	if err := s.loadLocked(); err != nil {
		unlock()
		return nil, err
	}
	return unlock, nil
}

func (s *FileStore) normalizeLegacyDataLocked() {
	for i := range s.data.Requests {
		request := &s.data.Requests[i]
		claimExpired := request.ClaimedAt.IsZero() ||
			!s.now().Before(request.ClaimedAt.Add(approvalClaimTTL))
		if (request.Status == StatusClaimed || request.Status == StatusProvisioning) && claimExpired {
			request.Status = StatusPending
			request.ApprovalClaimID = ""
			request.ClaimedBy = ""
			request.ClaimedAt = time.Time{}
		}
		if request.PrincipalID == "" {
			request.PrincipalID = PrincipalID(request.DiscordUserID)
		}
		if request.DiscordUserID == "" {
			request.DiscordUserID = string(request.PrincipalID)
		}
		s.upsertPrincipalLocked(request.PrincipalID, request.DiscordName, request.UpdatedAt)
	}

	latestApproval := make(map[PrincipalID]Request)
	for _, request := range s.data.Requests {
		if request.Status != StatusApproved {
			continue
		}
		if _, exists := s.activeGrantLocked(request.PrincipalID); exists {
			continue
		}
		current, exists := latestApproval[request.PrincipalID]
		if !exists || approvalTime(request).After(approvalTime(current)) {
			latestApproval[request.PrincipalID] = request
		}
	}

	for _, request := range s.data.Requests {
		latest, exists := latestApproval[request.PrincipalID]
		if !exists || latest.ID != request.ID {
			continue
		}
		startedAt := approvalTime(request)
		s.data.Grants = append(s.data.Grants, Grant{
			ID:                  "grant_" + request.ID,
			PrincipalID:         request.PrincipalID,
			RequestID:           request.ID,
			State:               GrantStateActive,
			AuthentikUserID:     request.AuthentikUserID,
			WireGuardClientID:   request.WireGuardClientID,
			IdentityBrokerOwned: false,
			WireGuardManaged:    true,
			StartsAt:            startedAt,
			CreatedAt:           startedAt,
			UpdatedAt:           request.UpdatedAt,
		})
	}
}

func approvalTime(request Request) time.Time {
	if !request.ReviewedAt.IsZero() {
		return request.ReviewedAt
	}
	return request.UpdatedAt
}

func (s *FileStore) upsertPrincipalLocked(id PrincipalID, displayName string, now time.Time) {
	if id == "" {
		return
	}
	for i := range s.data.Principals {
		if s.data.Principals[i].ID != id {
			continue
		}
		if displayName != "" {
			s.data.Principals[i].DisplayName = displayName
		}
		if now.After(s.data.Principals[i].UpdatedAt) {
			s.data.Principals[i].UpdatedAt = now
		}
		return
	}
	s.data.Principals = append(s.data.Principals, Principal{
		ID:          id,
		DisplayName: displayName,
		CreatedAt:   now,
		UpdatedAt:   now,
	})
}

func (s *FileStore) activeGrantLocked(principalID PrincipalID) (Grant, bool) {
	for _, grant := range s.data.Grants {
		if grant.PrincipalID == principalID && grant.BlocksNewGrant() {
			return grant, true
		}
	}
	return Grant{}, false
}

func (s *FileStore) snapshotLocked() storeData {
	return storeData{
		Requests:   append([]Request(nil), s.data.Requests...),
		Principals: append([]Principal(nil), s.data.Principals...),
		Grants:     append([]Grant(nil), s.data.Grants...),
	}
}

func requestPrincipalID(request Request) PrincipalID {
	if request.PrincipalID != "" {
		return request.PrincipalID
	}
	return PrincipalID(request.DiscordUserID)
}

func claimMatches(request Request, claim ApprovalClaim) bool {
	return request.ApprovalClaimID == claim.ID &&
		request.ClaimedBy == claim.ReviewerID &&
		request.ID == claim.Request.ID
}

func validateApprovalInput(input ApprovalInput) error {
	if input.ReviewerID == "" {
		return errors.New("reviewer id is required")
	}
	if input.WireGuardConfiguration == "" {
		return errors.New("wireguard configuration is required")
	}
	if input.DownloadToken == "" {
		return errors.New("download token is required")
	}
	if input.DownloadTokenExpiresAt.IsZero() {
		return errors.New("download token expiry is required")
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
