package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
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
	} else {
		var sendError *SendError
		if !errors.As(err, &sendError) || sendError.Ambiguous() || sendError.Retryable() {
			t.Fatalf("error = %#v", err)
		}
	}
}

func TestSendToUsesExplicitChatAndClassifiesHTTP429AsRetryable(t *testing.T) {
	var chatID string
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var body sendMessageRequest
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		chatID = body.ChatID
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Body:       io.NopCloser(strings.NewReader(`{"ok":false,"error_code":429,"description":"retry later"}`)),
			Header:     make(http.Header),
		}, nil
	})}
	client := NewWithEndpoint("https://telegram.test/sendMessage", "unused", httpjson.NewWithHTTPClient(httpClient, 1), nil)
	_, err := client.SendTo(context.Background(), "-2002", "message")
	var sendError *SendError
	if chatID != "-2002" || !errors.As(err, &sendError) || sendError.Ambiguous() || !sendError.Retryable() {
		t.Fatalf("chat=%q error=%#v", chatID, err)
	}
}

func TestSendToClassifiesHTTP500AsAmbiguous(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Body:       io.NopCloser(strings.NewReader(`{"ok":false,"error_code":500,"description":"internal"}`)),
			Header:     make(http.Header),
		}, nil
	})}
	client := NewWithEndpoint("https://telegram.test/sendMessage", "", httpjson.NewWithHTTPClient(httpClient, 1), nil)
	_, err := client.SendTo(context.Background(), "-1", "message")
	var sendError *SendError
	if !errors.As(err, &sendError) || !sendError.Ambiguous() || sendError.Retryable() {
		t.Fatalf("error=%#v", err)
	}
}

func TestSendToClassifiesTimeoutAsAmbiguousAndDialAsRetryable(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		ambiguous bool
		retryable bool
	}{
		{name: "timeout", err: timeoutError{}, ambiguous: true},
		{name: "dial", err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("refused")}, retryable: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return nil, test.err
			})}
			client := NewWithEndpoint("https://telegram.test/sendMessage", "", httpjson.NewWithHTTPClient(httpClient, 1), nil)
			_, err := client.SendTo(context.Background(), "-1", "message")
			var sendError *SendError
			if !errors.As(err, &sendError) || sendError.Ambiguous() != test.ambiguous || sendError.Retryable() != test.retryable {
				t.Fatalf("error=%#v", err)
			}
		})
	}
}

type timeoutError struct{}

func (timeoutError) Error() string   { return "timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
