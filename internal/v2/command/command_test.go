package command

import (
	"bytes"
	"context"
	"testing"
)

func TestCommandsHaveUniqueNames(t *testing.T) {
	seen := make(map[string]struct{})
	for _, command := range NewCommands(&bytes.Buffer{}, &bytes.Buffer{}) {
		if _, exists := seen[command.Name()]; exists {
			t.Fatalf("duplicate command %q", command.Name())
		}
		seen[command.Name()] = struct{}{}
	}
	for _, expected := range []string{"migrate", "worker", "api", "backfill"} {
		if _, exists := seen[expected]; !exists {
			t.Errorf("missing command %q", expected)
		}
	}
}

func TestHelpDoesNotRequireDatabase(t *testing.T) {
	for _, command := range NewCommands(&bytes.Buffer{}, &bytes.Buffer{}) {
		command.SetArgs([]string{"--help"})
		command.SetContext(context.Background())
		if err := command.Execute(); err != nil {
			t.Fatalf("%s --help error = %v", command.Name(), err)
		}
	}
}

func TestCommandsRejectPositionals(t *testing.T) {
	command := NewCommands(&bytes.Buffer{}, &bytes.Buffer{})[0]
	command.SetArgs([]string{"unexpected"})
	command.SetContext(context.Background())
	if err := command.Execute(); err == nil {
		t.Fatal("expected positional argument error")
	}
}
