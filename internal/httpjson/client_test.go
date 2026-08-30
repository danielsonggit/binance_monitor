package httpjson

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestNewWithProxyConfiguresExplicitProxy(t *testing.T) {
	client, err := NewWithProxy(time.Second, 1, "http://127.0.0.1:7890")
	if err != nil {
		t.Fatal(err)
	}
	transport, ok := client.httpClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T", client.httpClient.Transport)
	}
	request, err := http.NewRequest(http.MethodGet, "https://fapi.binance.com", nil)
	if err != nil {
		t.Fatal(err)
	}
	proxy, err := transport.Proxy(request)
	if err != nil {
		t.Fatal(err)
	}
	if proxy.String() != "http://127.0.0.1:7890" {
		t.Fatalf("proxy = %s", proxy)
	}
}

func TestNewWithProxyRejectsInvalidURL(t *testing.T) {
	if _, err := NewWithProxy(time.Second, 1, "://bad"); err == nil {
		t.Fatal("expected invalid proxy URL error")
	}
}

func TestJSONRetriesNetworkAndRetriableStatuses(t *testing.T) {
	var calls atomic.Int64
	client := NewWithHTTPClient(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		call := calls.Add(1)
		if call == 1 {
			return nil, errors.New("temporary network error")
		}
		status := http.StatusOK
		switch call {
		case 2:
			status = http.StatusTeapot
		case 3:
			status = http.StatusTooManyRequests
		case 4:
			status = http.StatusBadGateway
		}
		body := `{"ok":true}`
		if status != http.StatusOK {
			body = `{"error":"retry"}`
		}
		return &http.Response{
			StatusCode: status,
			Header:     http.Header{"Retry-After": []string{"0"}},
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    request,
		}, nil
	})}, 5)

	var result struct {
		OK bool `json:"ok"`
	}
	if err := client.JSON(context.Background(), http.MethodGet, "https://example.test", nil, nil, &result); err != nil {
		t.Fatal(err)
	}
	if !result.OK || calls.Load() != 5 {
		t.Fatalf("result=%#v calls=%d", result, calls.Load())
	}
}

func TestJSONDoesNotRetryPermanentClientError(t *testing.T) {
	var calls atomic.Int64
	client := NewWithHTTPClient(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"error":"bad request"}`)),
			Request:    request,
		}, nil
	})}, 4)

	err := client.JSON(context.Background(), http.MethodGet, "https://example.test", nil, nil, &struct{}{})
	var statusErr *StatusError
	if !errors.As(err, &statusErr) || statusErr.Code != http.StatusBadRequest {
		t.Fatalf("error = %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d", calls.Load())
	}
}

func TestJSONBoundsRetriesAndPreservesLastStatusError(t *testing.T) {
	var calls atomic.Int64
	client := NewWithHTTPClient(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Header:     http.Header{"Retry-After": []string{"0"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":"unavailable"}`)),
			Request:    request,
		}, nil
	})}, 3)

	err := client.JSON(context.Background(), http.MethodGet, "https://example.test", nil, nil, &struct{}{})
	var statusErr *StatusError
	if !errors.As(err, &statusErr) || statusErr.Code != http.StatusServiceUnavailable {
		t.Fatalf("error = %v", err)
	}
	if calls.Load() != 3 || !strings.Contains(err.Error(), "已尝试 3 次") {
		t.Fatalf("calls=%d error=%v", calls.Load(), err)
	}
}

func TestJSONCancelsRetryAfterWait(t *testing.T) {
	client := NewWithHTTPClient(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Header:     http.Header{"Retry-After": []string{"30"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":"slow down"}`)),
			Request:    request,
		}, nil
	})}, 3)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err := client.JSON(ctx, http.MethodGet, "https://example.test", nil, nil, &struct{}{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v", err)
	}
}

func TestParseRetryAfterSupportsSecondsAndHTTPDate(t *testing.T) {
	now := time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC)
	if delay, ok := parseRetryAfter("12", now); !ok || delay != 12*time.Second {
		t.Fatalf("seconds delay=%s ok=%v", delay, ok)
	}
	if delay, ok := parseRetryAfter(now.Add(5*time.Second).Format(http.TimeFormat), now); !ok || delay != 5*time.Second {
		t.Fatalf("date delay=%s ok=%v", delay, ok)
	}
	if _, ok := parseRetryAfter("invalid", now); ok {
		t.Fatal("invalid Retry-After accepted")
	}
}
