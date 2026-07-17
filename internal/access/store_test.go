package access

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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
