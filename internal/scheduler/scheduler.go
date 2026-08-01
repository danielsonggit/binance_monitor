package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"time"
)

type stateFile struct {
	LastSuccessfulSlot string `json:"last_successful_slot"`
	UpdatedAt          string `json:"updated_at"`
}

type Store struct {
	path string
}

func NewStore(path string) *Store {
	return &Store{path: path}
}

func SlotKey(value time.Time) string {
	return value.Format("2006-01-02T15:00-0700")
}

func DueSlot(
	now time.Time,
	reportHours []int,
	graceMinutes int,
	lastSuccessfulSlot string,
) (time.Time, bool) {
	if !slices.Contains(reportHours, now.Hour()) || now.Minute() >= graceMinutes {
		return time.Time{}, false
	}
	slot := time.Date(
		now.Year(),
		now.Month(),
		now.Day(),
		now.Hour(),
		0,
		0,
		0,
		now.Location(),
	)
	if SlotKey(slot) == lastSuccessfulSlot {
		return time.Time{}, false
	}
	return slot, true
}

func (s *Store) LastSuccessfulSlot() (string, error) {
	content, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("读取状态文件 %s: %w", s.path, err)
	}
	var state stateFile
	if err := json.Unmarshal(content, &state); err != nil {
		return "", fmt.Errorf("解析状态文件 %s: %w", s.path, err)
	}
	return state.LastSuccessfulSlot, nil
}

func (s *Store) MarkSuccess(slot time.Time) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("创建状态目录: %w", err)
	}
	content, err := json.MarshalIndent(
		stateFile{
			LastSuccessfulSlot: SlotKey(slot),
			UpdatedAt:          time.Now().In(slot.Location()).Format(time.RFC3339),
		},
		"",
		"  ",
	)
	if err != nil {
		return fmt.Errorf("编码状态文件: %w", err)
	}
	content = append(content, '\n')
	temporary := s.path + ".tmp"
	if err := os.WriteFile(temporary, content, 0o644); err != nil {
		return fmt.Errorf("写入临时状态文件: %w", err)
	}
	if err := os.Rename(temporary, s.path); err != nil {
		return fmt.Errorf("原子替换状态文件: %w", err)
	}
	return nil
}

func Run(
	ctx context.Context,
	job func(context.Context) error,
	location *time.Location,
	reportHours []int,
	graceMinutes int,
	store *Store,
	logger *slog.Logger,
) error {
	logger.Info(
		"调度器启动",
		"timezone", location.String(),
		"hours", reportHours,
		"grace_minutes", graceMinutes,
	)
	for {
		if err := runIfDue(ctx, job, location, reportHours, graceMinutes, store, logger); err != nil {
			logger.Error("调度检查失败", "error", err)
		}

		now := time.Now().In(location)
		wait := time.Minute - time.Duration(now.Second())*time.Second - time.Duration(now.Nanosecond())
		if wait < time.Second {
			wait = time.Second
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

func runIfDue(
	ctx context.Context,
	job func(context.Context) error,
	location *time.Location,
	reportHours []int,
	graceMinutes int,
	store *Store,
	logger *slog.Logger,
) error {
	lastSlot, err := store.LastSuccessfulSlot()
	if err != nil {
		return err
	}
	slot, due := DueSlot(time.Now().In(location), reportHours, graceMinutes, lastSlot)
	if !due {
		return nil
	}
	logger.Info("开始执行报告时点", "slot", SlotKey(slot))
	if err := job(ctx); err != nil {
		return fmt.Errorf("报告执行失败，宽限期内下一分钟重试: %w", err)
	}
	if err := store.MarkSuccess(slot); err != nil {
		return err
	}
	logger.Info("报告执行成功", "slot", SlotKey(slot))
	return nil
}
