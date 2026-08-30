package notification

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type Sender interface {
	SendTo(context.Context, string, string) (int64, error)
}

type ClassifiedSendError interface {
	error
	Ambiguous() bool
	Retryable() bool
}

type Dispatcher struct {
	repository Repository
	sender     Sender
	retryBase  time.Duration
	lease      time.Duration
}

func NewDispatcher(
	repository Repository,
	sender Sender,
	retryBase time.Duration,
	lease time.Duration,
) (*Dispatcher, error) {
	if repository == nil || sender == nil || retryBase <= 0 || lease <= 0 {
		return nil, fmt.Errorf("notification dispatcher 配置或依赖无效")
	}
	return &Dispatcher{
		repository: repository, sender: sender, retryBase: retryBase, lease: lease,
	}, nil
}

func (d *Dispatcher) Recover(ctx context.Context, now time.Time) error {
	return d.repository.RecoverStale(ctx, now.UTC().Add(-d.lease))
}

func (d *Dispatcher) DispatchOne(ctx context.Context, now time.Time) (bool, error) {
	job, found, err := d.repository.ClaimDue(ctx, now.UTC())
	if err != nil || !found {
		return found, err
	}
	for _, delivery := range job.Deliveries {
		if delivery.PartIndex < 0 || delivery.PartIndex >= len(job.Messages) {
			message := fmt.Sprintf("delivery part index 越界: %d", delivery.PartIndex)
			_ = d.repository.Dead(ctx, job.OutboxID, message)
			return true, errors.New(message)
		}
		if err := d.repository.MarkSending(ctx, job.OutboxID, delivery.ChatID, delivery.PartIndex); err != nil {
			return true, err
		}
		messageID, sendErr := d.sender.SendTo(ctx, delivery.ChatID, job.Messages[delivery.PartIndex])
		if sendErr == nil {
			if err := d.repository.MarkSent(ctx, job.OutboxID, delivery.ChatID, delivery.PartIndex, messageID); err != nil {
				// Telegram may already have accepted the message. Retrying here can
				// duplicate it, so persist UNKNOWN for manual reconciliation.
				_ = d.repository.MarkFailed(ctx, job.OutboxID, delivery.ChatID, delivery.PartIndex, err.Error(), true)
				_ = d.repository.Unknown(ctx, job.OutboxID, err.Error())
				return true, fmt.Errorf("记录 Telegram 成功结果失败: %w", err)
			}
			continue
		}
		ambiguous, retryable := classifySendError(sendErr)
		if err := d.repository.MarkFailed(ctx, job.OutboxID, delivery.ChatID, delivery.PartIndex, sendErr.Error(), ambiguous); err != nil {
			return true, err
		}
		if ambiguous {
			if err := d.repository.Unknown(ctx, job.OutboxID, sendErr.Error()); err != nil {
				return true, err
			}
			return true, sendErr
		}
		if !retryable || job.Attempt >= job.MaxAttempts {
			if err := d.repository.Dead(ctx, job.OutboxID, sendErr.Error()); err != nil {
				return true, err
			}
			return true, sendErr
		}
		nextAttempt := now.UTC().Add(d.retryDelay(job.Attempt))
		if err := d.repository.Retry(ctx, job.OutboxID, nextAttempt, sendErr.Error()); err != nil {
			return true, err
		}
		return true, sendErr
	}
	if err := d.repository.Complete(ctx, job.OutboxID, now.UTC()); err != nil {
		return true, err
	}
	return true, nil
}

func (d *Dispatcher) retryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := d.retryBase
	for range attempt - 1 {
		if delay >= time.Hour/2 {
			return time.Hour
		}
		delay *= 2
	}
	return delay
}

func classifySendError(err error) (ambiguous bool, retryable bool) {
	var classified ClassifiedSendError
	if !errors.As(err, &classified) {
		return true, false
	}
	return classified.Ambiguous(), classified.Retryable()
}
