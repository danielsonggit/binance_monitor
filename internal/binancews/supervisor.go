package binancews

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"binance-monitor/internal/domain/market"
)

var (
	ErrStreamStale         = errors.New("Binance WebSocket 行情超过新鲜度窗口")
	ErrRotate              = errors.New("Binance WebSocket 主动轮换连接")
	ErrEventsChannelClosed = errors.New("Binance WebSocket event channel closed")
	ErrErrorsChannelClosed = errors.New("Binance WebSocket error channel closed")
)

type Session interface {
	Events() <-chan []market.MiniTicker
	Errors() <-chan error
	Close() error
}

type Connector interface {
	Connect(context.Context) (Session, error)
}

type Sink interface {
	Apply([]market.MiniTicker)
}

type Status struct {
	Connected   bool
	LastEventAt time.Time
	LastError   string
}

type Supervisor struct {
	connector     Connector
	sink          Sink
	staleAfter    time.Duration
	rotateAfter   time.Duration
	reconnectWait time.Duration
	logger        *slog.Logger

	mu     sync.RWMutex
	status Status
}

func NewSupervisor(
	connector Connector,
	sink Sink,
	staleAfter time.Duration,
	rotateAfter time.Duration,
	reconnectWait time.Duration,
	logger *slog.Logger,
) *Supervisor {
	return &Supervisor{
		connector:     connector,
		sink:          sink,
		staleAfter:    staleAfter,
		rotateAfter:   rotateAfter,
		reconnectWait: reconnectWait,
		logger:        logger,
	}
}

func (s *Supervisor) Run(ctx context.Context) error {
	for {
		if ctx.Err() != nil {
			return nil
		}
		session, err := s.connector.Connect(ctx)
		if err != nil {
			s.setDisconnected(err)
			s.logger.Error("连接 Binance WebSocket 失败", "error", err)
			if !waitContext(ctx, s.reconnectWait) {
				return nil
			}
			continue
		}
		s.setConnected()
		s.logger.Info("Binance WebSocket 已连接")
		err = s.consume(ctx, session)
		if closeErr := session.Close(); closeErr != nil {
			s.logger.Warn("关闭 Binance WebSocket session 失败", "error", closeErr)
		}
		if ctx.Err() != nil {
			s.setDisconnected(nil)
			return nil
		}
		s.setDisconnected(err)
		s.logger.Warn("Binance WebSocket session 结束，准备重连", "error", err)
		if !waitContext(ctx, s.reconnectWait) {
			return nil
		}
	}
}

func (s *Supervisor) Status() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status
}

func (s *Supervisor) Health() (bool, time.Time, string) {
	status := s.Status()
	return status.Connected, status.LastEventAt, status.LastError
}

func (s *Supervisor) consume(ctx context.Context, session Session) error {
	staleTimer := time.NewTimer(s.staleAfter)
	defer staleTimer.Stop()
	rotationTimer := time.NewTimer(s.rotateAfter)
	defer rotationTimer.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case batch, ok := <-session.Events():
			if !ok {
				return ErrEventsChannelClosed
			}
			if len(batch) == 0 {
				continue
			}
			s.sink.Apply(batch)
			s.markEvent(time.Now().UTC())
			resetTimer(staleTimer, s.staleAfter)
		case err, ok := <-session.Errors():
			if !ok {
				return ErrErrorsChannelClosed
			}
			if err == nil {
				return fmt.Errorf("Binance WebSocket returned a nil error")
			}
			return err
		case <-staleTimer.C:
			return ErrStreamStale
		case <-rotationTimer.C:
			return ErrRotate
		}
	}
}

func (s *Supervisor) setConnected() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.Connected = true
	s.status.LastError = ""
}

func (s *Supervisor) setDisconnected(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.Connected = false
	if err == nil {
		s.status.LastError = ""
	} else {
		s.status.LastError = err.Error()
	}
}

func (s *Supervisor) markEvent(receivedAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.LastEventAt = receivedAt
}

func waitContext(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func resetTimer(timer *time.Timer, duration time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(duration)
}
