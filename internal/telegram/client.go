package telegram

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"

	"binance-monitor/internal/httpjson"
)

type Client struct {
	endpoint        string
	chatID          string
	messageThreadID *int64
	http            *httpjson.Client
}

type sendMessageRequest struct {
	ChatID             string             `json:"chat_id"`
	Text               string             `json:"text"`
	ParseMode          string             `json:"parse_mode"`
	LinkPreviewOptions linkPreviewOptions `json:"link_preview_options"`
	MessageThreadID    *int64             `json:"message_thread_id,omitempty"`
}

type linkPreviewOptions struct {
	IsDisabled bool `json:"is_disabled"`
}

type sendMessageResponse struct {
	OK          bool   `json:"ok"`
	ErrorCode   int    `json:"error_code"`
	Description string `json:"description"`
	Result      struct {
		MessageID int64 `json:"message_id"`
	} `json:"result"`
}

type SendError struct {
	err       error
	ambiguous bool
	retryable bool
}

func (e *SendError) Error() string   { return e.err.Error() }
func (e *SendError) Unwrap() error   { return e.err }
func (e *SendError) Ambiguous() bool { return e.ambiguous }
func (e *SendError) Retryable() bool { return e.retryable }

func New(
	botToken string,
	chatID string,
	httpClient *httpjson.Client,
	messageThreadID *int64,
) *Client {
	return &Client{
		endpoint:        "https://api.telegram.org/bot" + botToken + "/sendMessage",
		chatID:          chatID,
		messageThreadID: messageThreadID,
		http:            httpClient,
	}
}

func NewWithEndpoint(
	endpoint string,
	chatID string,
	httpClient *httpjson.Client,
	messageThreadID *int64,
) *Client {
	return &Client{
		endpoint:        endpoint,
		chatID:          chatID,
		messageThreadID: messageThreadID,
		http:            httpClient,
	}
}

func (c *Client) SendMessages(ctx context.Context, messages []string) ([]int64, error) {
	ids := make([]int64, 0, len(messages))
	for index, text := range messages {
		messageID, err := c.SendTo(ctx, c.chatID, text)
		if err != nil {
			return nil, fmt.Errorf("发送第 %d 条 Telegram 消息: %w", index+1, err)
		}
		ids = append(ids, messageID)
	}
	return ids, nil
}

func (c *Client) SendTo(ctx context.Context, chatID string, text string) (int64, error) {
	if c == nil || c.http == nil || chatID == "" || text == "" {
		return 0, &SendError{err: fmt.Errorf("Telegram sendMessage 参数无效"), retryable: false}
	}
	request := sendMessageRequest{
		ChatID:          chatID,
		Text:            text,
		ParseMode:       "HTML",
		MessageThreadID: c.messageThreadID,
		LinkPreviewOptions: linkPreviewOptions{
			IsDisabled: true,
		},
	}
	var response sendMessageResponse
	if err := c.http.JSON(ctx, http.MethodPost, c.endpoint, nil, request, &response); err != nil {
		var statusError *httpjson.StatusError
		if errors.As(err, &statusError) {
			ambiguous := statusError.Code == http.StatusRequestTimeout ||
				statusError.Code >= http.StatusInternalServerError
			return 0, &SendError{
				err:       fmt.Errorf("Telegram sendMessage HTTP 失败（%d）: %w", statusError.Code, err),
				ambiguous: ambiguous,
				retryable: statusError.Code == http.StatusTooManyRequests,
			}
		}
		if definitelyNotDelivered(err) {
			return 0, &SendError{
				err:       fmt.Errorf("Telegram sendMessage 连接失败: %w", err),
				retryable: true,
			}
		}
		return 0, &SendError{
			err: fmt.Errorf("Telegram sendMessage 传输结果不确定: %w", err),
			// A timeout can occur after Telegram accepted the request. Automatic
			// retry would risk a duplicate message.
			ambiguous: true,
		}
	}
	if !response.OK {
		ambiguous := response.ErrorCode == http.StatusRequestTimeout ||
			response.ErrorCode >= http.StatusInternalServerError
		return 0, &SendError{
			err:       fmt.Errorf("Telegram sendMessage 失败（%d）：%s", response.ErrorCode, response.Description),
			ambiguous: ambiguous,
			retryable: response.ErrorCode == http.StatusTooManyRequests,
		}
	}
	if response.Result.MessageID <= 0 {
		return 0, &SendError{err: fmt.Errorf("Telegram sendMessage 缺少 message_id"), ambiguous: true}
	}
	return response.Result.MessageID, nil
}

func definitelyNotDelivered(err error) bool {
	var dnsError *net.DNSError
	if errors.As(err, &dnsError) {
		return true
	}
	var operationError *net.OpError
	return errors.As(err, &operationError) && operationError.Op == "dial"
}
