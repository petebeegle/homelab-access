package access

import (
	"encoding/json"
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
