package wgeasy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/petebeegle/homelab-access/internal/access"
)

func TestProvisionClientCreatesAndDownloadsConfiguration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/session":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["username"] != "admin" || body["password"] != "password-1234" || body["remember"] != true {
				t.Fatalf("unexpected login body: %#v", body)
			}
			http.SetCookie(w, &http.Cookie{Name: "wg-easy", Value: "session-1", Secure: true})
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "success"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/client":
			if r.Header.Get("Cookie") != "wg-easy=session-1" {
				t.Fatalf("expected session cookie, got %q", r.Header.Get("Cookie"))
			}
			if r.URL.Query().Get("filter") != "discord-alice-example-user-1" {
				t.Fatalf("unexpected filter: %s", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode([]any{})
		case r.Method == http.MethodPost && r.URL.Path == "/api/client":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["name"] != "discord-alice-example-user-1" {
				t.Fatalf("unexpected client body: %#v", body)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "clientId": 9})
		case r.Method == http.MethodGet && r.URL.Path == "/api/client/9/configuration":
			_, _ = w.Write([]byte("[Interface]\nPrivateKey = test\n"))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	client := New(server.URL, "admin", "password-1234")
	result, err := client.ProvisionClient(context.Background(), access.Request{
		DiscordUserID: "user-1",
		DiscordName:   "Alice Example!",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ID != "9" {
		t.Fatalf("expected client id 9, got %q", result.ID)
	}
	if result.Configuration == "" {
		t.Fatal("expected wireguard configuration")
	}
}

func TestProvisionClientReusesExistingClient(t *testing.T) {
	var created bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/session":
			http.SetCookie(w, &http.Cookie{Name: "wg-easy", Value: "session-1", Secure: true})
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "success"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/client":
			if r.Header.Get("Cookie") != "wg-easy=session-1" {
				t.Fatalf("expected session cookie, got %q", r.Header.Get("Cookie"))
			}
			_ = json.NewEncoder(w).Encode([]any{
				map[string]any{"id": 4, "name": "discord-alice-user-1"},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/client":
			created = true
			w.WriteHeader(http.StatusInternalServerError)
		case r.Method == http.MethodGet && r.URL.Path == "/api/client/4/configuration":
			_, _ = w.Write([]byte("[Interface]\nPrivateKey = existing\n"))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	client := New(server.URL, "admin", "password-1234")
	result, err := client.ProvisionClient(context.Background(), access.Request{
		DiscordUserID: "user-1",
		DiscordName:   "Alice",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("expected existing client to be reused")
	}
	if result.ID != "4" {
		t.Fatalf("expected client id 4, got %q", result.ID)
	}
}

func TestClientNameSanitizesDiscordName(t *testing.T) {
	name := clientName(access.Request{
		DiscordUserID: "252951660027052033",
		DiscordName:   "Pete Beegle / Ops",
	})
	if name != "discord-pete-beegle-ops-052033" {
		t.Fatalf("unexpected client name: %q", name)
	}
}
