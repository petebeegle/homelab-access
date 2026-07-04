package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/petebeegle/homelab-access/internal/access"
	"github.com/petebeegle/homelab-access/internal/config"
	"github.com/petebeegle/homelab-access/internal/discord"
)

type Server struct {
	cfg       config.Config
	logger    *slog.Logger
	store     access.Store
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
	s := &Server{
		cfg:       cfg,
		logger:    logger,
		store:     store,
		startedAt: time.Now().UTC(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /readyz", s.readyz)
	mux.HandleFunc("GET /metrics", s.metrics)
	mux.HandleFunc("POST /discord/interactions", s.discordInteraction)
	mux.HandleFunc("GET /download/{token}", s.notImplemented("one-time VPN downloads are not implemented in the foundation build"))
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
	default:
		writeJSON(w, http.StatusOK, discord.EphemeralMessage("Unknown access command. Try `/access request`."))
	}
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
