package config

import (
	"errors"
	"os"
	"strings"
	"time"
)

type Config struct {
	HTTPAddr          string
	PublicBaseURL     string
	DiscordAppID      string
	DiscordPublicKey  string
	DiscordBotToken   string
	AuthentikBaseURL  string
	AuthentikToken    string
	WGEasyBaseURL     string
	WGEasyPassword    string
	StorePath         string
	DownloadTokenTTL  time.Duration
	ReadyWhenUnsealed bool
}

func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:         getEnv("HTTP_ADDR", ":8080"),
		PublicBaseURL:    strings.TrimRight(os.Getenv("PUBLIC_BASE_URL"), "/"),
		DiscordAppID:     os.Getenv("DISCORD_APP_ID"),
		DiscordPublicKey: os.Getenv("DISCORD_PUBLIC_KEY"),
		DiscordBotToken:  os.Getenv("DISCORD_BOT_TOKEN"),
		AuthentikBaseURL: strings.TrimRight(os.Getenv("AUTHENTIK_BASE_URL"), "/"),
		AuthentikToken:   os.Getenv("AUTHENTIK_TOKEN"),
		WGEasyBaseURL:    strings.TrimRight(os.Getenv("WGEASY_BASE_URL"), "/"),
		WGEasyPassword:   os.Getenv("WGEASY_PASSWORD"),
		StorePath:        getEnv("ACCESS_STORE_PATH", getEnv("DATABASE_PATH", "/data/homelab-access.json")),
	}

	ttl, err := time.ParseDuration(getEnv("DOWNLOAD_TOKEN_TTL", "15m"))
	if err != nil {
		return Config{}, err
	}
	cfg.DownloadTokenTTL = ttl
	cfg.ReadyWhenUnsealed = cfg.hasRuntimeSecrets()

	if cfg.HTTPAddr == "" {
		return Config{}, errors.New("HTTP_ADDR cannot be empty")
	}
	if cfg.StorePath == "" {
		return Config{}, errors.New("ACCESS_STORE_PATH cannot be empty")
	}

	return cfg, nil
}

func (c Config) MissingRuntimeKeys() []string {
	required := map[string]string{
		"PUBLIC_BASE_URL":    c.PublicBaseURL,
		"DISCORD_APP_ID":     c.DiscordAppID,
		"DISCORD_PUBLIC_KEY": c.DiscordPublicKey,
		"DISCORD_BOT_TOKEN":  c.DiscordBotToken,
		"AUTHENTIK_BASE_URL": c.AuthentikBaseURL,
		"AUTHENTIK_TOKEN":    c.AuthentikToken,
		"WGEASY_BASE_URL":    c.WGEasyBaseURL,
		"WGEASY_PASSWORD":    c.WGEasyPassword,
	}

	missing := make([]string, 0)
	for key, value := range required {
		if value == "" {
			missing = append(missing, key)
		}
	}
	return missing
}

func (c Config) hasRuntimeSecrets() bool {
	return len(c.MissingRuntimeKeys()) == 0
}

func getEnv(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}
