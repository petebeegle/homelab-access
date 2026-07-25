package commands

import (
	"errors"
	"net/http"
	"strings"

	"github.com/petebeegle/homelab-access/internal/discord"
)

var (
	ErrCommandPathRequired      = errors.New("command path is required")
	ErrHandlerRequired          = errors.New("command handler is required")
	ErrCommandAlreadyRegistered = errors.New("command is already registered")
)

type Handler interface {
	Handle(http.ResponseWriter, discord.Interaction)
}

type HandlerFunc func(http.ResponseWriter, discord.Interaction)

func (f HandlerFunc) Handle(w http.ResponseWriter, interaction discord.Interaction) {
	f(w, interaction)
}

type Dispatcher struct {
	handlers map[string]Handler
}

func NewDispatcher() *Dispatcher {
	return &Dispatcher{handlers: make(map[string]Handler)}
}

func (d *Dispatcher) Register(path string, handler Handler) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return ErrCommandPathRequired
	}
	if handler == nil {
		return ErrHandlerRequired
	}
	if _, exists := d.handlers[path]; exists {
		return ErrCommandAlreadyRegistered
	}
	d.handlers[path] = handler
	return nil
}

func (d *Dispatcher) Dispatch(w http.ResponseWriter, interaction discord.Interaction) bool {
	handler, ok := d.handlers[discord.CommandPath(interaction)]
	if !ok {
		return false
	}
	handler.Handle(w, interaction)
	return true
}
