package server

import (
	"crypto/ed25519"
	"encoding/hex"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/petebeegle/homelab-access/internal/config"
)

func TestHealthz(t *testing.T) {
	handler := New(config.Config{HTTPAddr: ":8080", DatabasePath: "/tmp/test.db"}, slog.Default())

	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, response.Code)
	}
	if !strings.Contains(response.Body.String(), `"status":"ok"`) {
		t.Fatalf("unexpected body: %s", response.Body.String())
	}
}

func TestReadyzReportsMissingRuntimeConfig(t *testing.T) {
	handler := New(config.Config{HTTPAddr: ":8080", DatabasePath: "/tmp/test.db"}, slog.Default())

	request := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status %d, got %d", http.StatusServiceUnavailable, response.Code)
	}
	if !strings.Contains(response.Body.String(), "DISCORD_BOT_TOKEN") {
		t.Fatalf("expected missing token in body: %s", response.Body.String())
	}
}

func TestMetrics(t *testing.T) {
	handler := New(config.Config{HTTPAddr: ":8080", DatabasePath: "/tmp/test.db"}, slog.Default())

	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, response.Code)
	}
	if !strings.Contains(response.Body.String(), "homelab_access_info") {
		t.Fatalf("unexpected body: %s", response.Body.String())
	}
}

func TestDiscordInteractionPing(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}

	body := `{"type":1}`
	handler := New(config.Config{
		HTTPAddr:         ":8080",
		DatabasePath:     "/tmp/test.db",
		DiscordPublicKey: hex.EncodeToString(publicKey),
	}, slog.Default())

	request := signedDiscordRequest(t, privateKey, body)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"type":1`) {
		t.Fatalf("unexpected body: %s", response.Body.String())
	}
}

func TestDiscordInteractionRejectsBadSignature(t *testing.T) {
	publicKey, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	_, wrongPrivateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}

	handler := New(config.Config{
		HTTPAddr:         ":8080",
		DatabasePath:     "/tmp/test.db",
		DiscordPublicKey: hex.EncodeToString(publicKey),
	}, slog.Default())

	request := signedDiscordRequest(t, wrongPrivateKey, `{"type":1}`)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d", http.StatusUnauthorized, response.Code)
	}
}

func TestDiscordAccessRequestCommand(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}

	body := `{
		"type": 2,
		"guild_id": "guild-1",
		"channel_id": "channel-1",
		"member": {
			"user": {"id": "user-1", "username": "alice"}
		},
		"data": {
			"name": "access",
			"options": [
				{
					"name": "request",
					"type": 1
				}
			]
		}
	}`
	handler := New(config.Config{
		HTTPAddr:         ":8080",
		DatabasePath:     "/tmp/test.db",
		DiscordPublicKey: hex.EncodeToString(publicKey),
	}, slog.Default())

	request := signedDiscordRequest(t, privateKey, body)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"flags":64`) {
		t.Fatalf("expected ephemeral response: %s", response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "Access request received") {
		t.Fatalf("unexpected body: %s", response.Body.String())
	}
}

func signedDiscordRequest(t *testing.T, privateKey ed25519.PrivateKey, body string) *http.Request {
	t.Helper()

	timestamp := "1700000000"
	signature := ed25519.Sign(privateKey, []byte(timestamp+body))
	request := httptest.NewRequest(http.MethodPost, "/discord/interactions", strings.NewReader(body))
	request.Header.Set("X-Signature-Timestamp", timestamp)
	request.Header.Set("X-Signature-Ed25519", hex.EncodeToString(signature))
	return request
}
