package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"binance-monitor/internal/notification"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type NotificationRepository struct {
	pool *pgxpool.Pool
}

type notificationPayload struct {
	Messages []string `json:"messages"`
	ChatIDs  []string `json:"chat_ids"`
}

func NewNotificationRepository(pool *pgxpool.Pool) *NotificationRepository {
	return &NotificationRepository{pool: pool}
}

func (r *NotificationRepository) Enqueue(
	ctx context.Context,
	request notification.EnqueueRequest,
) (notification.EnqueueResult, error) {
	if r == nil || r.pool == nil {
		return notification.EnqueueResult{}, fmt.Errorf("notification PostgreSQL pool 不能为空")
	}
	if len(request.Messages) == 0 || len(request.ChatIDs) == 0 {
		return notification.EnqueueResult{}, fmt.Errorf("notification 消息和接收人不能为空")
	}
	payload, err := json.Marshal(notificationPayload{Messages: request.Messages, ChatIDs: request.ChatIDs})
	if err != nil {
		return notification.EnqueueResult{}, fmt.Errorf("编码 notification payload: %w", err)
	}
	transaction, err := r.pool.Begin(ctx)
	if err != nil {
		return notification.EnqueueResult{}, fmt.Errorf("开始 notification enqueue 事务: %w", err)
	}
	defer func() { _ = transaction.Rollback(ctx) }()
	var id int64
	err = transaction.QueryRow(ctx, `
		INSERT INTO notification_outbox (
			idempotency_key, notification_type, scheduled_for, data_as_of,
			payload_json, status, max_attempts, next_attempt_at
		) VALUES ($1, $2, $3, $4, $5, 'PENDING', $6, $3)
		ON CONFLICT (idempotency_key) DO NOTHING
		RETURNING id`,
		request.IdempotencyKey, notification.ScheduledMarketReport,
		request.ScheduledFor.UTC(), request.DataAsOf.UTC(), payload, request.MaxAttempts,
	).Scan(&id)
	if err == nil {
		for _, chatID := range request.ChatIDs {
			for partIndex := range request.Messages {
				if _, err := transaction.Exec(ctx, `
					INSERT INTO notification_deliveries (outbox_id, chat_id, part_index, status)
					VALUES ($1, $2, $3, 'PENDING')`, id, chatID, partIndex); err != nil {
					return notification.EnqueueResult{}, fmt.Errorf("写入 notification delivery: %w", err)
				}
			}
		}
		if err := transaction.Commit(ctx); err != nil {
			return notification.EnqueueResult{}, fmt.Errorf("提交 notification enqueue: %w", err)
		}
		return notification.EnqueueResult{OutboxID: id, Created: true}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return notification.EnqueueResult{}, fmt.Errorf("写入 notification outbox: %w", err)
	}
	var scheduledFor, dataAsOf time.Time
	var existingPayload []byte
	var maxAttempts int
	if err := transaction.QueryRow(ctx, `
		SELECT id, scheduled_for, data_as_of, payload_json, max_attempts
		FROM notification_outbox WHERE idempotency_key = $1`, request.IdempotencyKey).
		Scan(&id, &scheduledFor, &dataAsOf, &existingPayload, &maxAttempts); err != nil {
		return notification.EnqueueResult{}, fmt.Errorf("查询已存在 notification outbox: %w", err)
	}
	var decoded notificationPayload
	if err := json.Unmarshal(existingPayload, &decoded); err != nil {
		return notification.EnqueueResult{}, fmt.Errorf("解析已存在 notification payload: %w", err)
	}
	if !scheduledFor.Equal(request.ScheduledFor) || !dataAsOf.Equal(request.DataAsOf) ||
		maxAttempts != request.MaxAttempts || !reflect.DeepEqual(decoded.Messages, request.Messages) ||
		!reflect.DeepEqual(decoded.ChatIDs, request.ChatIDs) {
		return notification.EnqueueResult{}, fmt.Errorf("notification idempotency key %s 内容冲突", request.IdempotencyKey)
	}
	if err := transaction.Commit(ctx); err != nil {
		return notification.EnqueueResult{}, fmt.Errorf("提交 notification replay: %w", err)
	}
	return notification.EnqueueResult{OutboxID: id, Created: false}, nil
}

func (r *NotificationRepository) ClaimDue(
	ctx context.Context,
	now time.Time,
) (notification.Job, bool, error) {
	transaction, err := r.pool.Begin(ctx)
	if err != nil {
		return notification.Job{}, false, fmt.Errorf("开始 notification claim 事务: %w", err)
	}
	defer func() { _ = transaction.Rollback(ctx) }()
	var job notification.Job
	var payload []byte
	err = transaction.QueryRow(ctx, `
		SELECT id, payload_json, attempts, max_attempts
		FROM notification_outbox
		WHERE status IN ('PENDING', 'RETRY')
			AND next_attempt_at <= $1
			AND attempts < max_attempts
		ORDER BY next_attempt_at, id
		FOR UPDATE SKIP LOCKED
		LIMIT 1`, now.UTC()).Scan(&job.OutboxID, &payload, &job.Attempt, &job.MaxAttempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return notification.Job{}, false, nil
	}
	if err != nil {
		return notification.Job{}, false, fmt.Errorf("claim notification outbox: %w", err)
	}
	job.Attempt++
	if _, err := transaction.Exec(ctx, `
		UPDATE notification_outbox
		SET status = 'PROCESSING', attempts = $2, locked_at = $3, updated_at = now(), last_error = NULL
		WHERE id = $1`, job.OutboxID, job.Attempt, now.UTC()); err != nil {
		return notification.Job{}, false, fmt.Errorf("标记 notification processing: %w", err)
	}
	var decoded notificationPayload
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return notification.Job{}, false, fmt.Errorf("解析 notification payload: %w", err)
	}
	if len(decoded.Messages) == 0 {
		return notification.Job{}, false, errors.New("notification payload 不包含消息分片")
	}
	job.Messages = decoded.Messages
	rows, err := transaction.Query(ctx, `
		SELECT chat_id, part_index, status
		FROM notification_deliveries
		WHERE outbox_id = $1 AND status IN ('PENDING', 'FAILED')
		ORDER BY chat_id, part_index`, job.OutboxID)
	if err != nil {
		return notification.Job{}, false, fmt.Errorf("查询待发送 notification deliveries: %w", err)
	}
	for rows.Next() {
		var delivery notification.Delivery
		if err := rows.Scan(&delivery.ChatID, &delivery.PartIndex, &delivery.Status); err != nil {
			rows.Close()
			return notification.Job{}, false, fmt.Errorf("读取 notification delivery: %w", err)
		}
		job.Deliveries = append(job.Deliveries, delivery)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return notification.Job{}, false, fmt.Errorf("遍历 notification deliveries: %w", err)
	}
	rows.Close()
	if err := transaction.Commit(ctx); err != nil {
		return notification.Job{}, false, fmt.Errorf("提交 notification claim: %w", err)
	}
	return job, true, nil
}

func (r *NotificationRepository) RecoverStale(ctx context.Context, before time.Time) error {
	transaction, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("开始 notification recovery: %w", err)
	}
	defer func() { _ = transaction.Rollback(ctx) }()
	if _, err := transaction.Exec(ctx, `
		UPDATE notification_deliveries d
		SET status = 'UNKNOWN', last_error = '进程在发送结果落库前退出', updated_at = now()
		FROM notification_outbox o
		WHERE d.outbox_id = o.id
			AND o.status = 'PROCESSING' AND o.locked_at < $1
			AND d.status = 'SENDING'`, before.UTC()); err != nil {
		return fmt.Errorf("恢复 SENDING notification delivery: %w", err)
	}
	if _, err := transaction.Exec(ctx, `
		UPDATE notification_outbox o
		SET status = CASE
				WHEN EXISTS (
					SELECT 1 FROM notification_deliveries d
					WHERE d.outbox_id = o.id AND d.status = 'UNKNOWN'
				) THEN 'UNKNOWN'
				ELSE 'RETRY'
			END,
			next_attempt_at = now(), locked_at = NULL,
			last_error = '恢复超时的 PROCESSING 任务', updated_at = now()
		WHERE o.status = 'PROCESSING' AND o.locked_at < $1`, before.UTC()); err != nil {
		return fmt.Errorf("恢复 PROCESSING notification outbox: %w", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return fmt.Errorf("提交 notification recovery: %w", err)
	}
	return nil
}

func (r *NotificationRepository) MarkSending(ctx context.Context, outboxID int64, chatID string, partIndex int) error {
	return r.updateDelivery(ctx, outboxID, chatID, partIndex, `
		UPDATE notification_deliveries
		SET status = 'SENDING', attempts = attempts + 1, last_error = NULL, updated_at = now()
		WHERE outbox_id = $1 AND chat_id = $2 AND part_index = $3 AND status IN ('PENDING', 'FAILED')`)
}

func (r *NotificationRepository) MarkSent(ctx context.Context, outboxID int64, chatID string, partIndex int, messageID int64) error {
	if messageID <= 0 {
		return fmt.Errorf("Telegram message id 必须大于 0")
	}
	tag, err := r.pool.Exec(ctx, `
		UPDATE notification_deliveries
		SET status = 'SENT', telegram_message_id = $4, last_error = NULL, updated_at = now()
		WHERE outbox_id = $1 AND chat_id = $2 AND part_index = $3 AND status = 'SENDING'`,
		outboxID, chatID, partIndex, messageID)
	if err != nil {
		return fmt.Errorf("标记 notification delivery sent: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("notification delivery sent 状态转换失败")
	}
	return nil
}

func (r *NotificationRepository) MarkFailed(
	ctx context.Context,
	outboxID int64,
	chatID string,
	partIndex int,
	message string,
	ambiguous bool,
) error {
	status := "FAILED"
	if ambiguous {
		status = "UNKNOWN"
	}
	tag, err := r.pool.Exec(ctx, `
		UPDATE notification_deliveries
		SET status = $4, telegram_message_id = NULL, last_error = $5, updated_at = now()
		WHERE outbox_id = $1 AND chat_id = $2 AND part_index = $3 AND status = 'SENDING'`,
		outboxID, chatID, partIndex, status, message)
	if err != nil {
		return fmt.Errorf("标记 notification delivery failed: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("notification delivery failed 状态转换失败")
	}
	return nil
}

func (r *NotificationRepository) Complete(ctx context.Context, outboxID int64, sentAt time.Time) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE notification_outbox o
		SET status = 'SENT', sent_at = $2, locked_at = NULL, last_error = NULL, updated_at = now()
		WHERE o.id = $1 AND o.status = 'PROCESSING'
			AND NOT EXISTS (
				SELECT 1 FROM notification_deliveries d
				WHERE d.outbox_id = o.id AND d.status <> 'SENT'
			)`, outboxID, sentAt.UTC())
	if err != nil {
		return fmt.Errorf("完成 notification outbox: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("notification outbox 尚有未成功 delivery")
	}
	return nil
}

func (r *NotificationRepository) Retry(ctx context.Context, outboxID int64, next time.Time, message string) error {
	return r.updateOutboxStatus(ctx, outboxID, "RETRY", next.UTC(), message)
}

func (r *NotificationRepository) Dead(ctx context.Context, outboxID int64, message string) error {
	return r.updateOutboxStatus(ctx, outboxID, "DEAD", time.Time{}, message)
}

func (r *NotificationRepository) Unknown(ctx context.Context, outboxID int64, message string) error {
	return r.updateOutboxStatus(ctx, outboxID, "UNKNOWN", time.Time{}, message)
}

func (r *NotificationRepository) updateDelivery(
	ctx context.Context,
	outboxID int64,
	chatID string,
	partIndex int,
	query string,
) error {
	tag, err := r.pool.Exec(ctx, query, outboxID, chatID, partIndex)
	if err != nil {
		return fmt.Errorf("更新 notification delivery: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("notification delivery 状态转换失败")
	}
	return nil
}

func (r *NotificationRepository) updateOutboxStatus(
	ctx context.Context,
	outboxID int64,
	status string,
	next time.Time,
	message string,
) error {
	var nextValue any
	if !next.IsZero() {
		nextValue = next.UTC()
	}
	tag, err := r.pool.Exec(ctx, `
		UPDATE notification_outbox
		SET status = $2,
			next_attempt_at = COALESCE($3, next_attempt_at),
			locked_at = NULL, last_error = $4, updated_at = now()
		WHERE id = $1 AND status = 'PROCESSING'`, outboxID, status, nextValue, message)
	if err != nil {
		return fmt.Errorf("更新 notification outbox %s: %w", status, err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("notification outbox %s 状态转换失败", status)
	}
	return nil
}
