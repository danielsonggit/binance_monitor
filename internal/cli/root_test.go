package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRootHelpListsV2Commands(t *testing.T) {
	var output bytes.Buffer
	if err := Execute(context.Background(), []string{"--help"}, &output, &output); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"worker", "api", "migrate", "backfill", "v1"} {
		if !strings.Contains(output.String(), name) {
			t.Errorf("help does not contain %q:\n%s", name, output.String())
		}
	}
}

func TestRootRejectsUnknownCommand(t *testing.T) {
	if err := Execute(context.Background(), []string{"unknown"}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Fatal("expected unknown command error")
	}
}

func TestV1ModeValidation(t *testing.T) {
	if err := Execute(
		context.Background(),
		[]string{"v1", "--once", "--daemon"},
		&bytes.Buffer{},
		&bytes.Buffer{},
	); err == nil {
		t.Fatal("expected mutually exclusive mode error")
	}
}
