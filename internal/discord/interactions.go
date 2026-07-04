package discord

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	InteractionTypePing               = 1
	InteractionTypeApplicationCommand = 2

	ResponseTypePong                     = 1
	ResponseTypeChannelMessageWithSource = 4

	MessageFlagEphemeral = 1 << 6
)

type Interaction struct {
	ID        string           `json:"id,omitempty"`
	Type      int              `json:"type"`
	Data      *InteractionData `json:"data,omitempty"`
	GuildID   string           `json:"guild_id,omitempty"`
	ChannelID string           `json:"channel_id,omitempty"`
	Member    *Member          `json:"member,omitempty"`
	User      *User            `json:"user,omitempty"`
}

type InteractionData struct {
	ID      string   `json:"id,omitempty"`
	Name    string   `json:"name,omitempty"`
	Type    int      `json:"type,omitempty"`
	Options []Option `json:"options,omitempty"`
}

type Option struct {
	Name    string          `json:"name"`
	Type    int             `json:"type"`
	Value   json.RawMessage `json:"value,omitempty"`
	Options []Option        `json:"options,omitempty"`
}

type Member struct {
	User  *User    `json:"user,omitempty"`
	Roles []string `json:"roles,omitempty"`
}

type User struct {
	ID            string `json:"id,omitempty"`
	Username      string `json:"username,omitempty"`
	GlobalName    string `json:"global_name,omitempty"`
	Discriminator string `json:"discriminator,omitempty"`
}

type InteractionResponse struct {
	Type int                      `json:"type"`
	Data *InteractionResponseData `json:"data,omitempty"`
}

type InteractionResponseData struct {
	Content string `json:"content,omitempty"`
	Flags   int    `json:"flags,omitempty"`
}

func ParseInteraction(body []byte) (Interaction, error) {
	var interaction Interaction
	if err := json.Unmarshal(body, &interaction); err != nil {
		return Interaction{}, err
	}
	if interaction.Type == 0 {
		return Interaction{}, errors.New("interaction type is required")
	}
	return interaction, nil
}

func VerifySignature(publicKeyHex, signatureHex, timestamp string, body []byte) error {
	if publicKeyHex == "" {
		return errors.New("discord public key is not configured")
	}
	if signatureHex == "" || timestamp == "" {
		return errors.New("discord signature headers are required")
	}

	publicKey, err := hex.DecodeString(publicKeyHex)
	if err != nil {
		return fmt.Errorf("decode discord public key: %w", err)
	}
	if len(publicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("discord public key must be %d bytes", ed25519.PublicKeySize)
	}

	signature, err := hex.DecodeString(signatureHex)
	if err != nil {
		return fmt.Errorf("decode discord signature: %w", err)
	}
	if len(signature) != ed25519.SignatureSize {
		return fmt.Errorf("discord signature must be %d bytes", ed25519.SignatureSize)
	}

	message := make([]byte, 0, len(timestamp)+len(body))
	message = append(message, timestamp...)
	message = append(message, body...)

	if !ed25519.Verify(ed25519.PublicKey(publicKey), message, signature) {
		return errors.New("invalid discord signature")
	}

	return nil
}

func UserID(interaction Interaction) string {
	if interaction.Member != nil && interaction.Member.User != nil {
		return interaction.Member.User.ID
	}
	if interaction.User != nil {
		return interaction.User.ID
	}
	return ""
}

func DisplayName(interaction Interaction) string {
	var user *User
	if interaction.Member != nil && interaction.Member.User != nil {
		user = interaction.Member.User
	} else {
		user = interaction.User
	}
	if user == nil {
		return ""
	}
	if user.GlobalName != "" {
		return user.GlobalName
	}
	if user.Username != "" {
		return user.Username
	}
	return user.ID
}

func CommandPath(interaction Interaction) string {
	if interaction.Data == nil {
		return ""
	}

	parts := []string{interaction.Data.Name}
	options := interaction.Data.Options
	for len(options) > 0 {
		current := options[0]
		if current.Type != 1 && current.Type != 2 {
			break
		}
		parts = append(parts, current.Name)
		options = current.Options
	}

	return strings.Join(parts, " ")
}

func StringOption(interaction Interaction, name string) string {
	options := commandLeafOptions(interaction)
	for _, option := range options {
		if option.Name != name {
			continue
		}
		var value string
		if err := json.Unmarshal(option.Value, &value); err != nil {
			return ""
		}
		return value
	}
	return ""
}

func commandLeafOptions(interaction Interaction) []Option {
	if interaction.Data == nil {
		return nil
	}

	options := interaction.Data.Options
	for len(options) > 0 {
		current := options[0]
		if current.Type != 1 && current.Type != 2 {
			break
		}
		options = current.Options
	}
	return options
}

func EphemeralMessage(content string) InteractionResponse {
	return InteractionResponse{
		Type: ResponseTypeChannelMessageWithSource,
		Data: &InteractionResponseData{
			Content: content,
			Flags:   MessageFlagEphemeral,
		},
	}
}
