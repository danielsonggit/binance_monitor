package command

import (
	"bytes"
	"context"
	"testing"
)

func TestRunRejectsMutuallyExclusiveModesBeforeConfiguration(t *testing.T) {
	err := Run(
		context.Background(),
		Options{Once: true, Daemon: true},
		&bytes.Buffer{},
		&bytes.Buffer{},
	)
	if err == nil {
		t.Fatal("expected mutually exclusive mode error")
	}
}
