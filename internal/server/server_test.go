package server

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/petebeegle/homelab-access/internal/config"
)

func TestHealthz(t *testing.T) {
	handler := testHandler(t, config.Config{HTTPAddr: ":8080"})

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
	handler := testHandler(t, config.Config{HTTPAddr: ":8080"})

	request := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status %d, got %d", http.StatusServiceUnavailable, response.Code)
	}
	if !strings.Contains(response.Body.String(), "DISCORD_PUBLIC_KEY") {
		t.Fatalf("expected missing discord public key in body: %s", response.Body.String())
	}
	if strings.Contains(response.Body.String(), "DISCORD_BOT_TOKEN") {
		t.Fatalf("did not expect future bot token to gate readiness: %s", response.Body.String())
	}
}

func TestReadyzRequiresOnlyImplementedRuntimeConfig(t *testing.T) {
	handler := testHandler(t, config.Config{
		HTTPAddr:         ":8080",
		PublicBaseURL:    "https://onboard.petebeegle.com",
		DiscordAppID:     "1523044601429102622",
		DiscordPublicKey: strings.Repeat("0", 64),
	})

	request := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, response.Code, response.Body.String())
	}
}

func TestMetrics(t *testing.T) {
	handler := testHandler(t, config.Config{HTTPAddr: ":8080"})

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

func TestDiscordOAuthCallbackRequiresClientSecret(t *testing.T) {
	handler := testHandler(t, config.Config{
		HTTPAddr:           ":8080",
		DiscordAppID:       "app-1",
		DiscordRedirectURI: "https://onboard.example.com/oauth/callback",
	})

	request := httptest.NewRequest(http.MethodGet, "/oauth/callback?code=code-1&guild_id=guild-1", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status %d, got %d: %s", http.StatusServiceUnavailable, response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "client secret") {
		t.Fatalf("expected client secret error, got: %s", response.Body.String())
	}
}

func TestDiscordOAuthCallbackExchangesCode(t *testing.T) {
	var tokenRequestBody string
	var authHeader string
	discordAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth2/token" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		authHeader = r.Header.Get("Authorization")
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		tokenRequestBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "access-token",
			"token_type":    "Bearer",
			"expires_in":    604800,
			"refresh_token": "refresh-token",
			"scope":         "bot applications.commands",
		})
	}))
	defer discordAPI.Close()

	handler := testHandler(t, config.Config{
		HTTPAddr:            ":8080",
		DiscordAppID:        "app-1",
		DiscordClientSecret: "secret-1",
		DiscordRedirectURI:  "https://onboard.example.com/oauth/callback",
		DiscordAPIBaseURL:   discordAPI.URL,
	})

	request := httptest.NewRequest(http.MethodGet, "/oauth/callback?code=code-1&guild_id=guild-1", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "Discord install complete") {
		t.Fatalf("unexpected body: %s", response.Body.String())
	}
	if authHeader == "" {
		t.Fatal("expected basic auth header")
	}
	if !strings.Contains(tokenRequestBody, "grant_type=authorization_code") {
		t.Fatalf("expected authorization_code grant, got: %s", tokenRequestBody)
	}
	if !strings.Contains(tokenRequestBody, "code=code-1") {
		t.Fatalf("expected code in token request, got: %s", tokenRequestBody)
	}
	if !strings.Contains(tokenRequestBody, "redirect_uri=https%3A%2F%2Fonboard.example.com%2Foauth%2Fcallback") {
		t.Fatalf("expected redirect_uri in token request, got: %s", tokenRequestBody)
	}
}

func TestDiscordInteractionPing(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}

	body := `{"type":1}`
	handler := testHandler(t, config.Config{
		HTTPAddr:         ":8080",
		DiscordPublicKey: hex.EncodeToString(publicKey),
	})

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

	handler := testHandler(t, config.Config{
		HTTPAddr:         ":8080",
		DiscordPublicKey: hex.EncodeToString(publicKey),
	})

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
	handler := testHandler(t, config.Config{
		HTTPAddr:         ":8080",
		DiscordPublicKey: hex.EncodeToString(publicKey),
	})

	request := signedDiscordRequest(t, privateKey, body)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"flags":64`) {
		t.Fatalf("expected ephemeral response: %s", response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "Access request req_") {
		t.Fatalf("unexpected body: %s", response.Body.String())
	}
}

func TestDiscordAccessRequestCommandReusesPendingRequest(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}

	body := `{
		"type": 2,
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
	handler := testHandler(t, config.Config{
		HTTPAddr:         ":8080",
		DiscordPublicKey: hex.EncodeToString(publicKey),
	})

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, signedDiscordRequest(t, privateKey, body))
	if first.Code != http.StatusOK {
		t.Fatalf("expected first status %d, got %d: %s", http.StatusOK, first.Code, first.Body.String())
	}

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, signedDiscordRequest(t, privateKey, body))
	if second.Code != http.StatusOK {
		t.Fatalf("expected second status %d, got %d: %s", http.StatusOK, second.Code, second.Body.String())
	}
	if !strings.Contains(second.Body.String(), "already have pending access request") {
		t.Fatalf("unexpected duplicate body: %s", second.Body.String())
	}
}

func TestDiscordAccessApproveCommand(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	authentikServer := fakeAuthentikServer(t)
	defer authentikServer.Close()

	handler := testHandler(t, config.Config{
		HTTPAddr:            ":8080",
		DiscordPublicKey:    hex.EncodeToString(publicKey),
		DiscordAdminUserIDs: []string{"admin-1"},
		AuthentikBaseURL:    authentikServer.URL,
		AuthentikToken:      "token-1",
	})

	createBody := `{
		"type": 2,
		"member": {
			"user": {"id": "user-1", "username": "alice"}
		},
		"data": {
			"name": "access",
			"options": [
				{"name": "request", "type": 1}
			]
		}
	}`
	createResponse := httptest.NewRecorder()
	handler.ServeHTTP(createResponse, signedDiscordRequest(t, privateKey, createBody))
	if createResponse.Code != http.StatusOK {
		t.Fatalf("expected create status %d, got %d: %s", http.StatusOK, createResponse.Code, createResponse.Body.String())
	}

	requestID := extractRequestID(t, createResponse.Body.String())
	approveBody := `{
		"type": 2,
		"member": {
			"user": {"id": "admin-1", "username": "admin"}
		},
		"data": {
			"name": "access",
			"options": [
				{
					"name": "approve",
					"type": 1,
					"options": [
						{"name": "request_id", "type": 3, "value": "` + requestID + `"}
					]
				}
			]
		}
	}`
	approveResponse := httptest.NewRecorder()
	handler.ServeHTTP(approveResponse, signedDiscordRequest(t, privateKey, approveBody))

	if approveResponse.Code != http.StatusOK {
		t.Fatalf("expected approve status %d, got %d: %s", http.StatusOK, approveResponse.Code, approveResponse.Body.String())
	}
	if !strings.Contains(approveResponse.Body.String(), "approved") {
		t.Fatalf("expected approval response, got: %s", approveResponse.Body.String())
	}

	duplicateResponse := httptest.NewRecorder()
	handler.ServeHTTP(duplicateResponse, signedDiscordRequest(t, privateKey, approveBody))
	if duplicateResponse.Code != http.StatusOK {
		t.Fatalf("expected duplicate status %d, got %d: %s", http.StatusOK, duplicateResponse.Code, duplicateResponse.Body.String())
	}
	if !strings.Contains(duplicateResponse.Body.String(), "already been reviewed") {
		t.Fatalf("expected already reviewed response, got: %s", duplicateResponse.Body.String())
	}
}

func TestDiscordAccessApproveCommandRejectsUnauthorizedUser(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	authentikServer := fakeAuthentikServer(t)
	defer authentikServer.Close()

	handler := testHandler(t, config.Config{
		HTTPAddr:            ":8080",
		DiscordPublicKey:    hex.EncodeToString(publicKey),
		DiscordAdminUserIDs: []string{"admin-1"},
		AuthentikBaseURL:    authentikServer.URL,
		AuthentikToken:      "token-1",
	})

	createBody := `{
		"type": 2,
		"member": {
			"user": {"id": "user-1", "username": "alice"}
		},
		"data": {
			"name": "access",
			"options": [
				{"name": "request", "type": 1}
			]
		}
	}`
	createResponse := httptest.NewRecorder()
	handler.ServeHTTP(createResponse, signedDiscordRequest(t, privateKey, createBody))
	if createResponse.Code != http.StatusOK {
		t.Fatalf("expected create status %d, got %d: %s", http.StatusOK, createResponse.Code, createResponse.Body.String())
	}

	requestID := extractRequestID(t, createResponse.Body.String())
	unauthorizedBody := accessReviewBody("approve", requestID, "user-2")
	unauthorizedResponse := httptest.NewRecorder()
	handler.ServeHTTP(unauthorizedResponse, signedDiscordRequest(t, privateKey, unauthorizedBody))
	if unauthorizedResponse.Code != http.StatusOK {
		t.Fatalf("expected unauthorized status %d, got %d: %s", http.StatusOK, unauthorizedResponse.Code, unauthorizedResponse.Body.String())
	}
	if !strings.Contains(unauthorizedResponse.Body.String(), "not allowed") {
		t.Fatalf("expected not allowed response, got: %s", unauthorizedResponse.Body.String())
	}

	approveResponse := httptest.NewRecorder()
	handler.ServeHTTP(approveResponse, signedDiscordRequest(t, privateKey, accessReviewBody("approve", requestID, "admin-1")))
	if approveResponse.Code != http.StatusOK {
		t.Fatalf("expected approve status %d, got %d: %s", http.StatusOK, approveResponse.Code, approveResponse.Body.String())
	}
	if !strings.Contains(approveResponse.Body.String(), "approved") {
		t.Fatalf("expected approval response after unauthorized attempt, got: %s", approveResponse.Body.String())
	}
}

func testHandler(t *testing.T, cfg config.Config) http.Handler {
	t.Helper()

	cfg.StorePath = t.TempDir() + "/requests.json"
	handler, err := New(cfg, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	return handler
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

func accessReviewBody(command, requestID, userID string) string {
	return `{
		"type": 2,
		"member": {
			"user": {"id": "` + userID + `", "username": "reviewer"}
		},
		"data": {
			"name": "access",
			"options": [
				{
					"name": "` + command + `",
					"type": 1,
					"options": [
						{"name": "request_id", "type": 3, "value": "` + requestID + `"}
					]
				}
			]
		}
	}`
}

func fakeAuthentikServer(t *testing.T) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token-1" {
			t.Fatalf("unexpected authorization header: %s", r.Header.Get("Authorization"))
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/core/users/":
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v3/core/users/":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"pk":        1,
				"username":  "discord-user-1",
				"name":      "alice",
				"is_active": true,
			})
		default:
			t.Fatalf("unexpected authentik request: %s %s", r.Method, r.URL.String())
		}
	}))
}

func extractRequestID(t *testing.T, body string) string {
	t.Helper()

	start := strings.Index(body, "req_")
	if start == -1 {
		t.Fatalf("request id not found in body: %s", body)
	}
	end := start
	for end < len(body) {
		current := body[end]
		if (current >= 'a' && current <= 'z') || (current >= '0' && current <= '9') || current == '_' {
			end++
			continue
		}
		break
	}
	return body[start:end]
}
