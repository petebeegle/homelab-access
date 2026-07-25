package commands

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/petebeegle/homelab-access/internal/discord"
)

func TestDispatcherRoutesCommandPath(t *testing.T) {
	dispatcher := NewDispatcher()
	called := false
	err := dispatcher.Register("access request", HandlerFunc(func(w http.ResponseWriter, interaction discord.Interaction) {
		called = true
		w.WriteHeader(http.StatusAccepted)
	}))
	if err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	handled := dispatcher.Dispatch(response, discord.Interaction{
		Data: &discord.InteractionData{
			Name: "access",
			Options: []discord.Option{{
				Name: "request",
				Type: 1,
			}},
		},
	})

	if !handled {
		t.Fatal("expected command to be handled")
	}
	if !called {
		t.Fatal("expected registered handler to run")
	}
	if response.Code != http.StatusAccepted {
		t.Fatalf("expected status %d, got %d", http.StatusAccepted, response.Code)
	}
}

func TestDispatcherLeavesUnknownCommandsUnhandled(t *testing.T) {
	dispatcher := NewDispatcher()
	if dispatcher.Dispatch(httptest.NewRecorder(), discord.Interaction{}) {
		t.Fatal("expected unknown command to remain unhandled")
	}
}

func TestDispatcherRejectsInvalidRegistrations(t *testing.T) {
	dispatcher := NewDispatcher()
	handler := HandlerFunc(func(http.ResponseWriter, discord.Interaction) {})

	if err := dispatcher.Register("", handler); !errors.Is(err, ErrCommandPathRequired) {
		t.Fatalf("expected ErrCommandPathRequired, got %v", err)
	}
	if err := dispatcher.Register("access request", nil); !errors.Is(err, ErrHandlerRequired) {
		t.Fatalf("expected ErrHandlerRequired, got %v", err)
	}
	if err := dispatcher.Register("access request", handler); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Register("access request", handler); !errors.Is(err, ErrCommandAlreadyRegistered) {
		t.Fatalf("expected ErrCommandAlreadyRegistered, got %v", err)
	}
}
