package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/petebeegle/homelab-access/internal/access"
	"github.com/petebeegle/homelab-access/internal/authentik"
	"github.com/petebeegle/homelab-access/internal/config"
	"github.com/petebeegle/homelab-access/internal/discord"
	"github.com/petebeegle/homelab-access/internal/wgeasy"
)

type Server struct {
	cfg       config.Config
	logger    *slog.Logger
	store     access.Store
	authentik *authentik.Client
	wgeasy    *wgeasy.Client
	startedAt time.Time
	requests  atomic.Uint64
}

func New(cfg config.Config, logger *slog.Logger) (http.Handler, error) {
	store, err := access.OpenFileStore(cfg.StorePath)
	if err != nil {
		return nil, err
	}

	return NewWithStore(cfg, logger, store), nil
}

func NewWithStore(cfg config.Config, logger *slog.Logger, store access.Store) http.Handler {
	if cfg.DownloadTokenTTL == 0 {
		cfg.DownloadTokenTTL = 15 * time.Minute
	}

	s := &Server{
		cfg:       cfg,
		logger:    logger,
		store:     store,
		authentik: authentik.New(cfg.AuthentikBaseURL, cfg.AuthentikToken),
		wgeasy:    wgeasy.New(cfg.WGEasyBaseURL, cfg.WGEasyUsername, cfg.WGEasyPassword),
		startedAt: time.Now().UTC(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /readyz", s.readyz)
	mux.HandleFunc("GET /metrics", s.metrics)
	mux.HandleFunc("POST /discord/interactions", s.discordInteraction)
	mux.HandleFunc("GET /oauth/callback", s.discordOAuthCallback)
	mux.HandleFunc("GET /download/{token}", s.confirmVPNConfigurationDownload)
	mux.HandleFunc("POST /download/{token}", s.downloadVPNConfiguration)
	mux.HandleFunc("/", s.notFound)

	return s.logRequests(mux)
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.requests.Add(1)
		next.ServeHTTP(w, r)
	})
}

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) readyz(w http.ResponseWriter, _ *http.Request) {
	missing := s.cfg.MissingRuntimeKeys()
	if len(missing) > 0 {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"status":  "not_ready",
			"missing": missing,
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) metrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	_, _ = w.Write([]byte("# HELP homelab_access_info Static service information.\n"))
	_, _ = w.Write([]byte("# TYPE homelab_access_info gauge\n"))
	_, _ = w.Write([]byte("homelab_access_info{version=\"foundation\"} 1\n"))
	_, _ = w.Write([]byte("# HELP homelab_access_http_requests_total HTTP requests handled by the service.\n"))
	_, _ = w.Write([]byte("# TYPE homelab_access_http_requests_total counter\n"))
	_, _ = w.Write([]byte("homelab_access_http_requests_total " + uintToString(s.requests.Load()) + "\n"))
}

func (s *Server) discordOAuthCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	guildID := r.URL.Query().Get("guild_id")
	if code == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing discord oauth code"})
		return
	}
	if s.cfg.DiscordClientSecret == "" {
		s.logger.Error("discord oauth callback cannot exchange code because DISCORD_CLIENT_SECRET is missing", "guild_id", guildID)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "discord client secret is not configured",
		})
		return
	}

	token, err := s.exchangeDiscordCode(r.Context(), code)
	if err != nil {
		s.logger.Error("discord oauth code exchange failed", "error", err, "guild_id", guildID)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "discord oauth code exchange failed"})
		return
	}

	s.logger.Info("discord oauth install completed", "guild_id", guildID, "scope", token.Scope, "expires_in", token.ExpiresIn)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("<!doctype html><title>Discord install complete</title><h1>Discord install complete</h1><p>You can close this tab and return to Discord.</p>"))
}

type discordTokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

func (s *Server) exchangeDiscordCode(ctx context.Context, code string) (discordTokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", s.cfg.DiscordRedirectURI)

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.DiscordAPIBaseURL+"/oauth2/token", strings.NewReader(form.Encode()))
	if err != nil {
		return discordTokenResponse{}, err
	}
	request.SetBasicAuth(s.cfg.DiscordAppID, s.cfg.DiscordClientSecret)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return discordTokenResponse{}, err
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return discordTokenResponse{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return discordTokenResponse{}, discordAPIError{StatusCode: response.StatusCode, Body: string(body)}
	}

	var token discordTokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return discordTokenResponse{}, err
	}
	return token, nil
}

type discordAPIError struct {
	StatusCode int
	Body       string
}

func (e discordAPIError) Error() string {
	return "discord api returned status " + http.StatusText(e.StatusCode) + ": " + e.Body
}

func (s *Server) discordInteraction(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		s.logger.Warn("failed to read discord interaction body", "error", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	if err := discord.VerifySignature(
		s.cfg.DiscordPublicKey,
		r.Header.Get("X-Signature-Ed25519"),
		r.Header.Get("X-Signature-Timestamp"),
		body,
	); err != nil {
		s.logger.Warn("invalid discord interaction signature", "error", err)
		http.Error(w, "invalid request signature", http.StatusUnauthorized)
		return
	}

	interaction, err := discord.ParseInteraction(body)
	if err != nil {
		s.logger.Warn("failed to parse discord interaction", "error", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid interaction payload"})
		return
	}

	switch interaction.Type {
	case discord.InteractionTypePing:
		writeJSON(w, http.StatusOK, discord.InteractionResponse{Type: discord.ResponseTypePong})
	case discord.InteractionTypeApplicationCommand:
		s.handleApplicationCommand(w, interaction)
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unsupported interaction type"})
	}
}

func (s *Server) handleApplicationCommand(w http.ResponseWriter, interaction discord.Interaction) {
	switch discord.CommandPath(interaction) {
	case "access request":
		userID := discord.UserID(interaction)
		if userID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "interaction user is required"})
			return
		}

		request, created, err := s.store.CreateOrGetPending(access.RequestInput{
			DiscordUserID: userID,
			DiscordName:   discord.DisplayName(interaction),
			GuildID:       interaction.GuildID,
			ChannelID:     interaction.ChannelID,
		})
		if err != nil {
			s.logger.Error("failed to create access request", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create access request"})
			return
		}

		s.logger.Info("access request received", "request_id", request.ID, "created", created, "discord_user_id", userID, "guild_id", interaction.GuildID, "channel_id", interaction.ChannelID)
		if created {
			writeJSON(w, http.StatusOK, discord.EphemeralMessage("Access request "+request.ID+" received. An admin will review it before any Authentik account or VPN configuration is created."))
			return
		}
		writeJSON(w, http.StatusOK, discord.EphemeralMessage("You already have pending access request "+request.ID+". An admin will review it before any Authentik account or VPN configuration is created."))
	case "access approve":
		s.reviewAccessRequest(w, interaction, access.StatusApproved)
	case "access deny":
		s.reviewAccessRequest(w, interaction, access.StatusDenied)
	default:
		writeJSON(w, http.StatusOK, discord.EphemeralMessage("Unknown access command. Try `/access request`."))
	}
}

func (s *Server) reviewAccessRequest(w http.ResponseWriter, interaction discord.Interaction, status string) {
	reviewerID := discord.UserID(interaction)
	if reviewerID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "interaction user is required"})
		return
	}
	if !s.canReviewAccess(interaction) {
		s.logger.Warn("discord user attempted access review without authorization", "reviewer_id", reviewerID, "guild_id", interaction.GuildID)
		writeJSON(w, http.StatusOK, discord.EphemeralMessage("You are not allowed to review access requests."))
		return
	}

	requestID := discord.StringOption(interaction, "request_id")
	if requestID == "" {
		writeJSON(w, http.StatusOK, discord.EphemeralMessage("Request ID is required."))
		return
	}

	var (
		request access.Request
		err     error
	)
	switch status {
	case access.StatusApproved:
		pendingRequest, err := s.store.GetPending(requestID)
		if err != nil {
			s.handleReviewError(w, err, requestID)
			return
		}
		user, created, err := s.authentik.EnsureUser(context.Background(), pendingRequest)
		if err != nil {
			s.logger.Error("failed to provision authentik user", "error", err, "request_id", requestID)
			writeJSON(w, http.StatusOK, discord.EphemeralMessage("Access request "+requestID+" is still pending because Authentik provisioning failed."))
			return
		}
		s.logger.Info("authentik user ensured", "request_id", requestID, "authentik_user_id", user.ID, "authentik_username", user.Username, "created", created)

		vpnClient, err := s.wgeasy.ProvisionClient(context.Background(), pendingRequest)
		if err != nil {
			s.logger.Error("failed to provision wireguard client", "error", err, "request_id", requestID)
			writeJSON(w, http.StatusOK, discord.EphemeralMessage("Access request "+requestID+" is still pending because VPN provisioning failed."))
			return
		}

		downloadToken, err := randomToken()
		if err != nil {
			s.logger.Error("failed to generate download token", "error", err, "request_id", requestID)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to generate download token"})
			return
		}

		request, err = s.store.Approve(requestID, access.ApprovalInput{
			ReviewerID:             reviewerID,
			AuthentikUserID:        intToString(user.ID),
			AuthentikUsername:      user.Username,
			WireGuardClientID:      vpnClient.ID,
			WireGuardConfiguration: vpnClient.Configuration,
			DownloadToken:          downloadToken,
			DownloadTokenExpiresAt: time.Now().UTC().Add(s.cfg.DownloadTokenTTL),
		})
	case access.StatusDenied:
		request, err = s.store.Deny(requestID, reviewerID)
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "unsupported review status"})
		return
	}

	if err != nil {
		s.handleReviewError(w, err, requestID)
		return
	}

	s.logger.Info("access request reviewed", "request_id", request.ID, "status", request.Status, "reviewer_id", reviewerID, "discord_user_id", request.DiscordUserID)
	if status == access.StatusApproved {
		downloadURL := s.cfg.PublicBaseURL + "/download/" + url.PathEscape(request.DownloadToken)
		writeJSON(w, http.StatusOK, discord.EphemeralMessage("Access request "+request.ID+" approved. Authentik user is ready. Open this link and click Download to retrieve the VPN config once: "+downloadURL))
		return
	}
	writeJSON(w, http.StatusOK, discord.EphemeralMessage("Access request "+request.ID+" denied."))
}

func (s *Server) confirmVPNConfigurationDownload(w http.ResponseWriter, r *http.Request) {
	setDownloadPageHeaders(w)
	if _, err := s.store.GetDownload(r.PathValue("token")); err != nil {
		writeDownloadError(w, err)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Download VPN configuration</title>
<style>
body{font:16px/1.5 system-ui,sans-serif;max-width:36rem;margin:10vh auto;padding:0 1.25rem;color:#171717}
button{font:inherit;font-weight:600;padding:.7rem 1rem;cursor:pointer}
</style>
</head>
<body>
<main>
<h1>Download VPN configuration</h1>
<p>This configuration can be downloaded once. Store it securely.</p>
<form method="post"><button type="submit">Download</button></form>
</main>
</body>
</html>`)
}

func (s *Server) downloadVPNConfiguration(w http.ResponseWriter, r *http.Request) {
	setDownloadPageHeaders(w)
	token := r.PathValue("token")
	request, err := s.store.ConsumeDownload(token)
	if err != nil {
		writeDownloadError(w, err)
		return
	}

	filename := "homelab-" + safeFilename(request.DiscordUserID) + ".conf"
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(request.WireGuardConfiguration))
}

func setDownloadPageHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
}

func writeDownloadError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, access.ErrDownloadExpired):
		writeJSON(w, http.StatusGone, map[string]string{"error": "download token expired"})
	case errors.Is(err, access.ErrDownloadConsumed):
		writeJSON(w, http.StatusGone, map[string]string{"error": "download token already used"})
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "download token not found"})
	}
}

func (s *Server) handleReviewError(w http.ResponseWriter, err error, requestID string) {
	switch {
	case errors.Is(err, access.ErrRequestNotFound):
		writeJSON(w, http.StatusOK, discord.EphemeralMessage("No access request found for "+requestID+"."))
	case errors.Is(err, access.ErrRequestNotPending):
		writeJSON(w, http.StatusOK, discord.EphemeralMessage("Access request "+requestID+" has already been reviewed."))
	default:
		s.logger.Error("failed to review access request", "error", err, "request_id", requestID)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to review access request"})
	}
}

func (s *Server) canReviewAccess(interaction discord.Interaction) bool {
	userID := discord.UserID(interaction)
	for _, adminUserID := range s.cfg.DiscordAdminUserIDs {
		if userID == adminUserID {
			return true
		}
	}
	if interaction.Member == nil {
		return false
	}
	for _, roleID := range interaction.Member.Roles {
		for _, adminRoleID := range s.cfg.DiscordAdminRoleIDs {
			if roleID == adminRoleID {
				return true
			}
		}
	}
	return false
}

func (s *Server) notImplemented(message string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusNotImplemented, map[string]string{
			"status":  "not_implemented",
			"message": message,
		})
	}
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/healthz") || strings.HasPrefix(r.URL.Path, "/readyz") {
		writeJSON(w, http.StatusNotFound, map[string]string{"status": "not_found"})
		return
	}
	http.NotFound(w, r)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func uintToString(value uint64) string {
	if value == 0 {
		return "0"
	}

	var buf [20]byte
	i := len(buf)
	for value > 0 {
		i--
		buf[i] = byte('0' + value%10)
		value /= 10
	}
	return string(buf[i:])
}

func intToString(value int) string {
	if value == 0 {
		return ""
	}
	return uintToString(uint64(value))
}

func randomToken() (string, error) {
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(token[:]), nil
}

func safeFilename(value string) string {
	var builder strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			builder.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			builder.WriteRune(r)
		case r >= '0' && r <= '9':
			builder.WriteRune(r)
		case r == '-' || r == '_':
			builder.WriteRune(r)
		}
	}
	if builder.Len() == 0 {
		return "wireguard"
	}
	return builder.String()
}
