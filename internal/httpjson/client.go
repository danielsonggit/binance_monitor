package httpjson

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const maxErrorBody = 500

type Client struct {
	httpClient *http.Client
	maxRetries int
}

type StatusError struct {
	Code int
	URL  string
	Body string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("HTTP %d：%s；响应：%s", e.Code, e.URL, e.Body)
}

func New(timeout time.Duration, maxRetries int) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: timeout},
		maxRetries: maxRetries,
	}
}

func NewWithProxy(timeout time.Duration, maxRetries int, proxyURL string) (*Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if proxyURL != "" {
		parsed, err := url.Parse(proxyURL)
		if err != nil {
			return nil, fmt.Errorf("解析 HTTP proxy URL: %w", err)
		}
		transport.Proxy = http.ProxyURL(parsed)
	}
	return &Client{
		httpClient: &http.Client{
			Timeout:   timeout,
			Transport: transport,
		},
		maxRetries: maxRetries,
	}, nil
}

func NewWithHTTPClient(client *http.Client, maxRetries int) *Client {
	return &Client{httpClient: client, maxRetries: maxRetries}
}

func (c *Client) JSON(
	ctx context.Context,
	method string,
	endpoint string,
	query url.Values,
	body any,
	out any,
) error {
	if query != nil {
		parsed, err := url.Parse(endpoint)
		if err != nil {
			return fmt.Errorf("解析 URL: %w", err)
		}
		values := parsed.Query()
		for key, items := range query {
			for _, item := range items {
				values.Add(key, item)
			}
		}
		parsed.RawQuery = values.Encode()
		endpoint = parsed.String()
	}

	var encoded []byte
	var err error
	if body != nil {
		encoded, err = json.Marshal(body)
		if err != nil {
			return fmt.Errorf("编码 JSON 请求: %w", err)
		}
	}

	var lastErr error
	for attempt := 0; attempt < c.maxRetries; attempt++ {
		request, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(encoded))
		if err != nil {
			return fmt.Errorf("创建 HTTP 请求: %w", err)
		}
		request.Header.Set("Accept", "application/json")
		request.Header.Set("User-Agent", "binance-market-reporter/1.0")
		if body != nil {
			request.Header.Set("Content-Type", "application/json")
		}

		response, err := c.httpClient.Do(request)
		if err != nil {
			lastErr = err
		} else {
			payload, readErr := io.ReadAll(io.LimitReader(response.Body, 8<<20))
			closeErr := response.Body.Close()
			if readErr != nil {
				lastErr = readErr
			} else if closeErr != nil {
				lastErr = closeErr
			} else if response.StatusCode < 200 || response.StatusCode >= 300 {
				detail := string(payload)
				if len(detail) > maxErrorBody {
					detail = detail[:maxErrorBody]
				}
				statusErr := &StatusError{Code: response.StatusCode, URL: endpoint, Body: detail}
				lastErr = statusErr
				if response.StatusCode != http.StatusTooManyRequests && response.StatusCode < 500 {
					return statusErr
				}
			} else if err := json.Unmarshal(payload, out); err != nil {
				return fmt.Errorf("解析 %s 的 JSON 响应: %w", endpoint, err)
			} else {
				return nil
			}
		}

		if attempt+1 < c.maxRetries {
			delay := time.Duration(1<<attempt) * time.Second
			if delay > 4*time.Second {
				delay = 4 * time.Second
			}
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	return fmt.Errorf("请求失败（已尝试 %d 次）：%s；%w", c.maxRetries, endpoint, lastErr)
}
