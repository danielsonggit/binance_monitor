package watchdog

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"strings"
	"time"
)

type Notifier interface {
	Send(context.Context, string, string) error
}

type ambiguousNotificationError interface {
	Ambiguous() bool
}

type Service struct {
	probe             Probe
	store             StateStore
	notifier          Notifier
	chatIDs           []string
	failureThreshold  int
	recoveryThreshold int
	pollEvery         time.Duration
	location          *time.Location
	logger            *slog.Logger
}

func NewService(
	probe Probe,
	store StateStore,
	notifier Notifier,
	chatIDs []string,
	failureThreshold int,
	recoveryThreshold int,
	pollEvery time.Duration,
	location *time.Location,
	logger *slog.Logger,
) (*Service, error) {
	if probe == nil || store == nil || notifier == nil || logger == nil || location == nil {
		return nil, fmt.Errorf("watchdog 依赖不能为空")
	}
	if len(chatIDs) == 0 || failureThreshold <= 0 || recoveryThreshold <= 0 || pollEvery <= 0 {
		return nil, fmt.Errorf("watchdog 阈值或接收人无效")
	}
	return &Service{
		probe: probe, store: store, notifier: notifier, chatIDs: append([]string(nil), chatIDs...),
		failureThreshold: failureThreshold, recoveryThreshold: recoveryThreshold,
		pollEvery: pollEvery, location: location, logger: logger,
	}, nil
}

func (s *Service) Run(ctx context.Context) error {
	if _, _, err := s.RunOnce(ctx); err != nil {
		return err
	}
	ticker := time.NewTicker(s.pollEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if _, _, err := s.RunOnce(ctx); err != nil {
				s.logger.Error("watchdog 检查失败", "error", err)
			}
		}
	}
}

func (s *Service) RunOnce(ctx context.Context) (ProbeResult, State, error) {
	state, err := s.store.Load(ctx)
	if err != nil {
		return ProbeResult{}, State{}, err
	}
	initializeMaps(&state)
	result := s.probe.Check(ctx)
	state.LastCheckedAt = result.CheckedAt.UTC()
	if result.Healthy {
		err = s.handleHealthy(ctx, result, &state)
	} else {
		err = s.handleFailure(ctx, result, &state)
	}
	if err != nil {
		return result, state, err
	}
	if err := s.store.Save(ctx, state); err != nil {
		return result, state, err
	}
	return result, state, nil
}

func (s *Service) handleFailure(ctx context.Context, result ProbeResult, state *State) error {
	state.FailureCount++
	state.RecoveryCount = 0
	state.LastReason = result.Reason()
	if state.StartedAt.IsZero() {
		state.StartedAt = result.CheckedAt.UTC()
	}
	if !state.Active && state.FailureCount >= s.failureThreshold {
		state.Active = true
	}
	if !state.Active {
		s.logger.Warn("watchdog 检测到暂时异常", "failure_count", state.FailureCount, "reason", state.LastReason)
		return nil
	}
	message := s.alertMessage(*state)
	for _, chatID := range s.chatIDs {
		if state.AlertSent[chatID] {
			continue
		}
		delivered, err := s.send(ctx, chatID, message)
		if delivered {
			state.AlertSent[chatID] = true
		}
		if err != nil {
			s.logger.Error("watchdog 故障通知失败", "chat_id", chatID, "error", err)
		}
	}
	s.logger.Error("watchdog incident 生效", "failure_count", state.FailureCount, "reason", state.LastReason)
	return nil
}

func (s *Service) handleHealthy(ctx context.Context, result ProbeResult, state *State) error {
	if !state.Active {
		*state = State{LastCheckedAt: result.CheckedAt.UTC()}
		initializeMaps(state)
		s.logger.Info("watchdog 检查健康")
		return nil
	}
	state.RecoveryCount++
	if state.RecoveryCount < s.recoveryThreshold {
		s.logger.Info("watchdog 等待稳定恢复", "recovery_count", state.RecoveryCount)
		return nil
	}
	message := s.recoveryMessage(*state, result.CheckedAt)
	for _, chatID := range s.chatIDs {
		if state.RecoverySent[chatID] {
			continue
		}
		delivered, err := s.send(ctx, chatID, message)
		if delivered {
			state.RecoverySent[chatID] = true
		}
		if err != nil {
			s.logger.Error("watchdog 恢复通知失败", "chat_id", chatID, "error", err)
		}
	}
	if allDelivered(s.chatIDs, state.RecoverySent) {
		*state = State{LastCheckedAt: result.CheckedAt.UTC()}
		initializeMaps(state)
		s.logger.Info("watchdog incident 已恢复")
	}
	return nil
}

func (s *Service) send(ctx context.Context, chatID, message string) (bool, error) {
	err := s.notifier.Send(ctx, chatID, message)
	if err == nil {
		return true, nil
	}
	if ambiguous, ok := err.(ambiguousNotificationError); ok && ambiguous.Ambiguous() {
		return true, err
	}
	return false, err
}

func (s *Service) alertMessage(state State) string {
	return fmt.Sprintf(
		"🚨 <b>Binance Radar V2 故障</b>\n\n开始时间：%s\n连续失败：%d 次\n原因：%s\n\nV1 不一定受影响，请检查 jmk、7890、PostgreSQL 与 V2 服务。",
		s.formatTime(state.StartedAt), state.FailureCount, html.EscapeString(trimReason(state.LastReason)),
	)
}

func (s *Service) recoveryMessage(state State, recoveredAt time.Time) string {
	duration := recoveredAt.Sub(state.StartedAt)
	if duration < 0 {
		duration = 0
	}
	return fmt.Sprintf(
		"✅ <b>Binance Radar V2 已恢复</b>\n\n故障开始：%s\n恢复时间：%s\n持续时间：%s\n最后原因：%s",
		s.formatTime(state.StartedAt), s.formatTime(recoveredAt), duration.Round(time.Second),
		html.EscapeString(trimReason(state.LastReason)),
	)
}

func (s *Service) formatTime(value time.Time) string {
	return value.In(s.location).Format("2006-01-02 15:04:05 MST")
}

func trimReason(value string) string {
	value = strings.TrimSpace(value)
	if len([]rune(value)) <= 1000 {
		return value
	}
	return string([]rune(value)[:1000]) + "…"
}

func allDelivered(chatIDs []string, delivered map[string]bool) bool {
	for _, chatID := range chatIDs {
		if !delivered[chatID] {
			return false
		}
	}
	return true
}
