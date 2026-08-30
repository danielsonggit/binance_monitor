package watchdog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type State struct {
	Active        bool            `json:"active"`
	FailureCount  int             `json:"failure_count"`
	RecoveryCount int             `json:"recovery_count"`
	StartedAt     time.Time       `json:"started_at,omitempty"`
	LastCheckedAt time.Time       `json:"last_checked_at,omitempty"`
	LastReason    string          `json:"last_reason,omitempty"`
	AlertSent     map[string]bool `json:"alert_sent,omitempty"`
	RecoverySent  map[string]bool `json:"recovery_sent,omitempty"`
}

type StateStore interface {
	Load(context.Context) (State, error)
	Save(context.Context, State) error
}

type FileStore struct {
	path string
}

func NewFileStore(path string) (*FileStore, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("watchdog state file 必须是绝对路径")
	}
	return &FileStore{path: path}, nil
}

func (s *FileStore) Load(ctx context.Context) (State, error) {
	if err := ctx.Err(); err != nil {
		return State{}, err
	}
	payload, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return State{}, nil
	}
	if err != nil {
		return State{}, fmt.Errorf("读取 watchdog state: %w", err)
	}
	var state State
	if err := json.Unmarshal(payload, &state); err != nil {
		return State{}, fmt.Errorf("解析 watchdog state: %w", err)
	}
	initializeMaps(&state)
	return state, nil
}

func (s *FileStore) Save(ctx context.Context, state State) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	initializeMaps(&state)
	payload, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("编码 watchdog state: %w", err)
	}
	directory := filepath.Dir(s.path)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return fmt.Errorf("创建 watchdog state 目录: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".watchdog-*.tmp")
	if err != nil {
		return fmt.Errorf("创建 watchdog 临时 state: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("设置 watchdog state 权限: %w", err)
	}
	if _, err := temporary.Write(payload); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("写入 watchdog state: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("同步 watchdog state: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("关闭 watchdog state: %w", err)
	}
	if err := os.Rename(temporaryPath, s.path); err != nil {
		return fmt.Errorf("替换 watchdog state: %w", err)
	}
	return nil
}

func initializeMaps(state *State) {
	if state.AlertSent == nil {
		state.AlertSent = make(map[string]bool)
	}
	if state.RecoverySent == nil {
		state.RecoverySent = make(map[string]bool)
	}
}
