package access

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestFileStoreCreateOrGetPending(t *testing.T) {
	path := filepath.Join(t.TempDir(), "requests.json")
	store, err := OpenFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	store.now = func() time.Time { return time.Date(2026, 7, 4, 19, 15, 0, 0, time.UTC) }

	first, created, err := store.CreateOrGetPending(RequestInput{
		DiscordUserID: "user-1",
		DiscordName:   "alice",
		GuildID:       "guild-1",
		ChannelID:     "channel-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("expected first request to be created")
	}
	if first.ID == "" {
		t.Fatal("expected request id")
	}
	if first.Status != StatusPending {
		t.Fatalf("expected pending status, got %q", first.Status)
	}

	second, created, err := store.CreateOrGetPending(RequestInput{DiscordUserID: "user-1"})
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("expected duplicate pending request to be reused")
	}
	if second.ID != first.ID {
		t.Fatalf("expected duplicate to return %q, got %q", first.ID, second.ID)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var data storeData
	if err := json.Unmarshal(content, &data); err != nil {
		t.Fatal(err)
	}
	if len(data.Requests) != 1 {
		t.Fatalf("expected one persisted request, got %d", len(data.Requests))
	}
}

func TestFileStoreReloadsRequests(t *testing.T) {
	path := filepath.Join(t.TempDir(), "requests.json")
	store, err := OpenFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	request, _, err := store.CreateOrGetPending(RequestInput{DiscordUserID: "user-1"})
	if err != nil {
		t.Fatal(err)
	}

	reloaded, err := OpenFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, created, err := reloaded.CreateOrGetPending(RequestInput{DiscordUserID: "user-1"})
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("expected reloaded pending request to be reused")
	}
	if duplicate.ID != request.ID {
		t.Fatalf("expected %q, got %q", request.ID, duplicate.ID)
	}
}

func TestFileStoreReviewsPendingRequest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "requests.json")
	store, err := OpenFileStore(path)
	if err != nil {
		t.Fatal(err)
	}

	createdAt := time.Date(2026, 7, 4, 19, 15, 0, 0, time.UTC)
	reviewedAt := createdAt.Add(5 * time.Minute)
	store.now = func() time.Time { return createdAt }
	request, _, err := store.CreateOrGetPending(RequestInput{DiscordUserID: "user-1"})
	if err != nil {
		t.Fatal(err)
	}

	store.now = func() time.Time { return reviewedAt }
	pending, err := store.GetPending(request.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pending.ID != request.ID {
		t.Fatalf("expected pending request %q, got %q", request.ID, pending.ID)
	}

	approved, err := store.Approve(request.ID, ApprovalInput{
		ReviewerID:             "admin-1",
		AuthentikUserID:        "42",
		AuthentikUsername:      "discord-user-1",
		IdentityBrokerOwned:    true,
		WireGuardClientID:      "7",
		WireGuardConfiguration: "[Interface]\nPrivateKey = test\n",
		DownloadToken:          "token-1",
		DownloadTokenExpiresAt: reviewedAt.Add(15 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if approved.Status != StatusApproved {
		t.Fatalf("expected approved status, got %q", approved.Status)
	}
	if approved.ReviewedBy != "admin-1" {
		t.Fatalf("expected reviewer admin-1, got %q", approved.ReviewedBy)
	}
	if !approved.ReviewedAt.Equal(reviewedAt) {
		t.Fatalf("expected reviewed_at %s, got %s", reviewedAt, approved.ReviewedAt)
	}
	if approved.WireGuardClientID != "7" {
		t.Fatalf("expected wireguard client id, got %q", approved.WireGuardClientID)
	}
	grant, err := store.GetActiveGrant(PrincipalID("user-1"))
	if err != nil {
		t.Fatal(err)
	}
	if !grant.IdentityBrokerOwned || !grant.WireGuardManaged {
		t.Fatalf("expected provider ownership metadata, got %#v", grant)
	}

	if _, err := store.Deny(request.ID, "admin-2"); !errors.Is(err, ErrRequestNotPending) {
		t.Fatalf("expected ErrRequestNotPending, got %v", err)
	}
	if _, err := store.GetPending(request.ID); !errors.Is(err, ErrRequestNotPending) {
		t.Fatalf("expected reviewed request to stop being pending, got %v", err)
	}
}

func TestFileStoreConsumesDownloadOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "requests.json")
	store, err := OpenFileStore(path)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 7, 4, 19, 15, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	request, _, err := store.CreateOrGetPending(RequestInput{DiscordUserID: "user-1"})
	if err != nil {
		t.Fatal(err)
	}
	approved, err := store.Approve(request.ID, ApprovalInput{
		ReviewerID:             "admin-1",
		WireGuardClientID:      "7",
		WireGuardConfiguration: "[Interface]\nPrivateKey = test\n",
		DownloadToken:          "token-1",
		DownloadTokenExpiresAt: now.Add(15 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}

	store.now = func() time.Time { return now.Add(time.Minute) }
	preview, err := store.GetDownload(approved.DownloadToken)
	if err != nil {
		t.Fatal(err)
	}
	if preview.WireGuardConfiguration == "" {
		t.Fatal("expected wireguard configuration in preview lookup")
	}
	if _, err := store.GetDownload(approved.DownloadToken); err != nil {
		t.Fatalf("expected repeated preview lookup to succeed, got %v", err)
	}

	download, err := store.ConsumeDownload(approved.DownloadToken)
	if err != nil {
		t.Fatal(err)
	}
	if download.WireGuardConfiguration == "" {
		t.Fatal("expected wireguard configuration")
	}

	if _, err := store.ConsumeDownload(approved.DownloadToken); !errors.Is(err, ErrDownloadConsumed) {
		t.Fatalf("expected ErrDownloadConsumed, got %v", err)
	}
}

func TestFileStoreApprovalClaimIsAtomic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "requests.json")
	store, err := OpenFileStore(path)
	if err != nil {
		t.Fatal(err)
	}

	request, _, err := store.CreateOrGetPending(RequestInput{
		DiscordUserID: "user-1",
		DiscordName:   "alice",
	})
	if err != nil {
		t.Fatal(err)
	}

	const contenders = 32
	start := make(chan struct{})
	var successes atomic.Int32
	var failures atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < contenders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, claimErr := store.ClaimApproval(request.ID, "admin-1")
			switch {
			case claimErr == nil:
				successes.Add(1)
			case errors.Is(claimErr, ErrRequestNotPending):
				failures.Add(1)
			default:
				t.Errorf("unexpected claim error: %v", claimErr)
			}
		}()
	}
	close(start)
	wg.Wait()

	if successes.Load() != 1 {
		t.Fatalf("expected exactly one successful claim, got %d", successes.Load())
	}
	if failures.Load() != contenders-1 {
		t.Fatalf("expected %d rejected claims, got %d", contenders-1, failures.Load())
	}
}

func TestFileStoreApprovalClaimIsAtomicAcrossInstances(t *testing.T) {
	path := filepath.Join(t.TempDir(), "requests.json")
	firstStore, err := OpenFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	request, _, err := firstStore.CreateOrGetPending(RequestInput{
		DiscordUserID: "user-1",
		DiscordName:   "alice",
	})
	if err != nil {
		t.Fatal(err)
	}
	secondStore, err := OpenFileStore(path)
	if err != nil {
		t.Fatal(err)
	}

	stores := []*FileStore{firstStore, secondStore}
	start := make(chan struct{})
	results := make(chan error, len(stores))
	for i, store := range stores {
		go func(store *FileStore, reviewer string) {
			<-start
			_, claimErr := store.ClaimApproval(request.ID, reviewer)
			results <- claimErr
		}(store, "admin-"+strconv.Itoa(i+1))
	}
	close(start)

	successes := 0
	rejections := 0
	for range stores {
		switch claimErr := <-results; {
		case claimErr == nil:
			successes++
		case errors.Is(claimErr, ErrApprovalInProgress):
			rejections++
		default:
			t.Fatalf("unexpected claim error: %v", claimErr)
		}
	}
	if successes != 1 || rejections != 1 {
		t.Fatalf("expected one claim and one rejection, got %d and %d", successes, rejections)
	}
}

func TestFileStoreSerializesClaimsAcrossRequestsForOnePrincipal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "requests.json")
	now := time.Now().UTC()
	data := storeData{Requests: []Request{
		{
			ID:            "req_one",
			PrincipalID:   PrincipalID("user-1"),
			DiscordUserID: "user-1",
			Status:        StatusPending,
			CreatedAt:     now,
			UpdatedAt:     now,
		},
		{
			ID:            "req_two",
			PrincipalID:   PrincipalID("user-1"),
			DiscordUserID: "user-1",
			Status:        StatusPending,
			CreatedAt:     now,
			UpdatedAt:     now,
		},
	}}
	content, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}

	firstStore, err := OpenFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	secondStore, err := OpenFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	type attempt struct {
		store     *FileStore
		requestID string
	}
	attempts := []attempt{
		{store: firstStore, requestID: "req_one"},
		{store: secondStore, requestID: "req_two"},
	}
	start := make(chan struct{})
	results := make(chan error, len(attempts))
	for _, candidate := range attempts {
		go func(candidate attempt) {
			<-start
			_, claimErr := candidate.store.ClaimApproval(candidate.requestID, "admin-1")
			results <- claimErr
		}(candidate)
	}
	close(start)

	successes := 0
	rejections := 0
	for range attempts {
		switch claimErr := <-results; {
		case claimErr == nil:
			successes++
		case errors.Is(claimErr, ErrApprovalInProgress):
			rejections++
		default:
			t.Fatalf("unexpected claim error: %v", claimErr)
		}
	}
	if successes != 1 || rejections != 1 {
		t.Fatalf("expected one principal claim and one rejection, got %d and %d", successes, rejections)
	}
}

func TestFileStoreReleaseApprovalClaimRestoresPendingRequest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "requests.json")
	store, err := OpenFileStore(path)
	if err != nil {
		t.Fatal(err)
	}

	request, _, err := store.CreateOrGetPending(RequestInput{DiscordUserID: "user-1"})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := store.ClaimApproval(request.ID, "admin-1")
	if err != nil {
		t.Fatal(err)
	}
	provisioning, err := store.BeginApprovalProvisioning(claim)
	if err != nil {
		t.Fatal(err)
	}
	if provisioning.Status != StatusProvisioning {
		t.Fatalf("expected provisioning status, got %q", provisioning.Status)
	}

	if err := store.ReleaseApprovalClaim(request.ID, "wrong-claim"); !errors.Is(err, ErrApprovalClaimMismatch) {
		t.Fatalf("expected ErrApprovalClaimMismatch, got %v", err)
	}
	if err := store.ReleaseApprovalClaim(request.ID, claim.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetPending(request.ID); err != nil {
		t.Fatalf("expected request to become pending again, got %v", err)
	}
}

func TestFileStoreFailedApprovalDoesNotReopenAfterExternalSuccess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "requests.json")
	store, err := OpenFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	request, _, err := store.CreateOrGetPending(RequestInput{DiscordUserID: "user-1"})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := store.ClaimApproval(request.ID, "admin-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.BeginApprovalProvisioning(claim); err != nil {
		t.Fatal(err)
	}

	failed, err := store.FailApproval(claim)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != StatusFailed {
		t.Fatalf("expected failed status, got %q", failed.Status)
	}
	if _, err := store.GetPending(request.ID); !errors.Is(err, ErrRequestNotPending) {
		t.Fatalf("expected failed request to stay closed, got %v", err)
	}
	if _, err := store.ClaimApproval(request.ID, "admin-2"); !errors.Is(err, ErrRequestNotPending) {
		t.Fatalf("expected failed request not to be re-claimed, got %v", err)
	}
}

func TestFileStoreAllowsOnlyOneActiveGrantPerPrincipal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "requests.json")
	store, err := OpenFileStore(path)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	request, _, err := store.CreateOrGetPending(RequestInput{
		DiscordUserID: "user-1",
		DiscordName:   "alice",
	})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := store.ClaimApproval(request.ID, "admin-1")
	if err != nil {
		t.Fatal(err)
	}
	approval := ApprovalInput{
		ReviewerID:             "admin-1",
		AuthentikUserID:        "42",
		AuthentikUsername:      "discord-user-1",
		IdentityBrokerOwned:    true,
		WireGuardClientID:      "7",
		WireGuardConfiguration: "[Interface]\nPrivateKey = test\n",
		DownloadToken:          "token-1",
		DownloadTokenExpiresAt: now.Add(15 * time.Minute),
	}
	if _, err := store.FinalizeApproval(claim, approval); !errors.Is(err, ErrRequestNotProvisioning) {
		t.Fatalf("expected ErrRequestNotProvisioning before provisioning, got %v", err)
	}
	if _, err := store.BeginApprovalProvisioning(claim); err != nil {
		t.Fatal(err)
	}
	approved, err := store.FinalizeApproval(claim, approval)
	if err != nil {
		t.Fatal(err)
	}

	grant, err := store.GetActiveGrant(PrincipalID("user-1"))
	if err != nil {
		t.Fatal(err)
	}
	if grant.PrincipalID != PrincipalID("user-1") {
		t.Fatalf("expected stable principal user-1, got %q", grant.PrincipalID)
	}
	if grant.RequestID != approved.ID {
		t.Fatalf("expected grant request %q, got %q", approved.ID, grant.RequestID)
	}
	if grant.State != GrantStateActive {
		t.Fatalf("expected active grant, got %q", grant.State)
	}

	if _, _, err := store.CreateOrGetPending(RequestInput{
		DiscordUserID: "user-1",
		DiscordName:   "alice-renamed",
	}); !errors.Is(err, ErrActiveGrantExists) {
		t.Fatalf("expected ErrActiveGrantExists, got %v", err)
	}
}

func TestFileStoreLoadsLegacyRequestJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "requests.json")
	legacy := `{
  "requests": [
    {
      "id": "req_legacy",
      "discord_user_id": "user-1",
      "discord_name": "alice",
      "status": "approved",
      "authentik_user_id": "42",
      "wireguard_client_id": "7",
      "created_at": "2026-07-25T12:00:00Z",
      "updated_at": "2026-07-25T12:05:00Z"
    }
  ]
}
`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := OpenFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := store.GetActiveGrant(PrincipalID("user-1"))
	if err != nil {
		t.Fatal(err)
	}
	if grant.RequestID != "req_legacy" {
		t.Fatalf("expected legacy request ownership, got %q", grant.RequestID)
	}
	if grant.IdentityBrokerOwned {
		t.Fatal("legacy identity ownership must remain unproven")
	}
	if !grant.WireGuardManaged {
		t.Fatal("expected legacy grant to retain broker-managed VPN access")
	}
	if _, _, err := store.CreateOrGetPending(RequestInput{DiscordUserID: "user-1"}); !errors.Is(err, ErrActiveGrantExists) {
		t.Fatalf("expected legacy approval to enforce active grant, got %v", err)
	}
}

func TestFileStoreLoadsNewestLegacyApprovalAsActiveGrant(t *testing.T) {
	path := filepath.Join(t.TempDir(), "requests.json")
	legacy := `{
  "requests": [
    {
      "id": "req_old",
      "discord_user_id": "user-1",
      "status": "approved",
      "wireguard_client_id": "old-peer",
      "reviewed_at": "2026-07-24T12:00:00Z",
      "created_at": "2026-07-24T11:00:00Z",
      "updated_at": "2026-07-24T12:00:00Z"
    },
    {
      "id": "req_new",
      "discord_user_id": "user-1",
      "status": "approved",
      "wireguard_client_id": "new-peer",
      "reviewed_at": "2026-07-25T12:00:00Z",
      "created_at": "2026-07-25T11:00:00Z",
      "updated_at": "2026-07-25T12:00:00Z"
    }
  ]
}
`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := OpenFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := store.GetActiveGrant(PrincipalID("user-1"))
	if err != nil {
		t.Fatal(err)
	}
	if grant.RequestID != "req_new" || grant.WireGuardClientID != "new-peer" {
		t.Fatalf("expected newest legacy approval, got %#v", grant)
	}
}

func TestFileStoreRecoversInterruptedApprovalAsPending(t *testing.T) {
	path := filepath.Join(t.TempDir(), "requests.json")
	interrupted := `{
  "requests": [
    {
      "id": "req_interrupted",
      "principal_id": "user-1",
      "discord_user_id": "user-1",
      "status": "provisioning",
      "approval_claim_id": "claim-stale",
      "claimed_by": "admin-1",
      "claimed_at": "2026-07-25T12:01:00Z",
      "created_at": "2026-07-25T12:00:00Z",
      "updated_at": "2026-07-25T12:01:00Z"
    }
  ]
}
`
	if err := os.WriteFile(path, []byte(interrupted), 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := OpenFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := store.GetPending("req_interrupted")
	if err != nil {
		t.Fatalf("expected interrupted request to recover as pending, got %v", err)
	}
	if recovered.ApprovalClaimID != "" || recovered.ClaimedBy != "" || !recovered.ClaimedAt.IsZero() {
		t.Fatalf("expected interrupted claim metadata to be cleared, got %#v", recovered)
	}
	if _, err := store.ClaimApproval(recovered.ID, "admin-2"); err != nil {
		t.Fatalf("expected recovered request to be claimable, got %v", err)
	}
}

func TestFileStorePreservesFreshApprovalClaimAcrossInstances(t *testing.T) {
	path := filepath.Join(t.TempDir(), "requests.json")
	firstStore, err := OpenFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	request, _, err := firstStore.CreateOrGetPending(RequestInput{DiscordUserID: "user-1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := firstStore.ClaimApproval(request.ID, "admin-1"); err != nil {
		t.Fatal(err)
	}

	secondStore, err := OpenFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := secondStore.GetPending(request.ID); !errors.Is(err, ErrRequestNotPending) {
		t.Fatalf("expected fresh claim to remain in progress, got %v", err)
	}
}

func TestFileStoreTracksMutableDisplayNameUnderStablePrincipal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "requests.json")
	store, err := OpenFileStore(path)
	if err != nil {
		t.Fatal(err)
	}

	first, _, err := store.CreateOrGetPending(RequestInput{
		DiscordUserID: "user-1",
		DiscordName:   "alice",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Deny(first.ID, "admin-1"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CreateOrGetPending(RequestInput{
		DiscordUserID: "user-1",
		DiscordName:   "alice-renamed",
	}); err != nil {
		t.Fatal(err)
	}

	principal, err := store.GetPrincipal(PrincipalID("user-1"))
	if err != nil {
		t.Fatal(err)
	}
	if principal.ID != PrincipalID("user-1") {
		t.Fatalf("expected stable Discord principal ID, got %q", principal.ID)
	}
	if principal.DisplayName != "alice-renamed" {
		t.Fatalf("expected current display name, got %q", principal.DisplayName)
	}
}
