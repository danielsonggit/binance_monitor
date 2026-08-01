package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"binance-monitor/internal/httpjson"
)

func TestSendMessagesUsesHTMLAndThreadID(t *testing.T) {
	var requests []sendMessageRequest
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var body sendMessageRequest
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		requests = append(requests, body)
		var encoded bytes.Buffer
		if err := json.NewEncoder(&encoded).Encode(map[string]any{
			"ok": true,
			"result": map[string]any{
				"message_id": len(requests) + 9,
			},
		}); err != nil {
			t.Fatal(err)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(&encoded),
			Header:     make(http.Header),
		}, nil
	})}

	threadID := int64(99)
	client := NewWithEndpoint(
		"https://telegram.test/sendMessage",
		"-1001",
		httpjson.NewWithHTTPClient(httpClient, 1),
		&threadID,
	)
	ids, err := client.SendMessages(context.Background(), []string{"first", "second"})
	if err != nil {
		t.Fatalf("SendMessages() error = %v", err)
	}
	if len(ids) != 2 || ids[0] != 10 || ids[1] != 11 {
		t.Fatalf("ids = %v", ids)
	}
	if requests[0].ParseMode != "HTML" {
		t.Errorf("parse mode = %q", requests[0].ParseMode)
	}
	if requests[0].MessageThreadID == nil || *requests[0].MessageThreadID != 99 {
		t.Errorf("thread ID = %v", requests[0].MessageThreadID)
	}
	if !requests[0].LinkPreviewOptions.IsDisabled {
		t.Error("link previews should be disabled")
	}
}

func TestSendMessagesRejectsTelegramFailure(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(
				`{"ok":false,"description":"chat not found"}`,
			)),
			Header: make(http.Header),
		}, nil
	})}

	client := NewWithEndpoint(
		"https://telegram.test/sendMessage",
		"-1001",
		httpjson.NewWithHTTPClient(httpClient, 1),
		nil,
	)
	if _, err := client.SendMessages(context.Background(), []string{"message"}); err == nil {
		t.Fatal("expected error")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
