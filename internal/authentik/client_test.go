package authentik

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/petebeegle/homelab-access/internal/access"
)

func TestEnsureUserCreatesMissingUser(t *testing.T) {
	var sawCreate bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token-1" {
			t.Fatalf("unexpected authorization header: %s", r.Header.Get("Authorization"))
		}

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/core/users/":
			if r.URL.Query().Get("search") != "discord-user-1" {
				t.Fatalf("unexpected search query: %s", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v3/core/users/":
			sawCreate = true
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload["username"] != "discord-user-1" {
				t.Fatalf("unexpected username: %v", payload["username"])
			}
			if payload["name"] != "Alice" {
				t.Fatalf("unexpected name: %v", payload["name"])
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(User{ID: 42, Username: "discord-user-1", Name: "Alice", IsActive: true})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	client := New(server.URL, "token-1")
	user, created, err := client.EnsureUser(context.Background(), access.Request{
		DiscordUserID: "user-1",
		DiscordName:   "Alice",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("expected user to be created")
	}
	if !sawCreate {
		t.Fatal("expected create request")
	}
	if user.ID != 42 {
		t.Fatalf("expected user id 42, got %d", user.ID)
	}
}

func TestEnsureUserReturnsExistingUser(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v3/core/users/" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []User{
				{ID: 7, Username: "someone-else"},
				{ID: 8, Username: "discord-user-1", Name: "Alice", IsActive: true},
			},
		})
	}))
	defer server.Close()

	client := New(server.URL, "token-1")
	user, created, err := client.EnsureUser(context.Background(), access.Request{DiscordUserID: "user-1"})
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("expected existing user to be reused")
	}
	if user.ID != 8 {
		t.Fatalf("expected user id 8, got %d", user.ID)
	}
}
