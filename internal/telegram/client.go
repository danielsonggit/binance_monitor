package telegram

import (
	"context"
	"fmt"
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
	Description string `json:"description"`
	Result      struct {
		MessageID int64 `json:"message_id"`
	} `json:"result"`
}

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
		request := sendMessageRequest{
			ChatID:          c.chatID,
			Text:            text,
			ParseMode:       "HTML",
			MessageThreadID: c.messageThreadID,
			LinkPreviewOptions: linkPreviewOptions{
				IsDisabled: true,
			},
		}
		var response sendMessageResponse
		if err := c.http.JSON(
			ctx,
			http.MethodPost,
			c.endpoint,
			nil,
			request,
			&response,
		); err != nil {
			return nil, fmt.Errorf("发送第 %d 条 Telegram 消息: %w", index+1, err)
		}
		if !response.OK {
			return nil, fmt.Errorf(
				"Telegram sendMessage 失败：%s",
				response.Description,
			)
		}
		ids = append(ids, response.Result.MessageID)
	}
	return ids, nil
}
