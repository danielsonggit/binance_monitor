package watchdog

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileStoreRoundTripAndPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "watchdog.json")
	store, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	want := State{Active: true, FailureCount: 3, StartedAt: time.Now().UTC(), AlertSent: map[string]bool{"-1": true}}
	if err := store.Save(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !got.Active || got.FailureCount != 3 || !got.AlertSent["-1"] || !got.StartedAt.Equal(want.StartedAt) {
		t.Fatalf("state = %#v", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("permissions = %o", info.Mode().Perm())
	}
}

func TestFileStoreRejectsRelativePath(t *testing.T) {
	if _, err := NewFileStore("state/watchdog.json"); err == nil {
		t.Fatal("expected absolute path error")
	}
}
