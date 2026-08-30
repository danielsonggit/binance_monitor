package binancews

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestSDKConnectorReceivesLiveMiniTickersIntegration(t *testing.T) {
	proxyURL := os.Getenv("BINANCE_WS_TEST_PROXY")
	if proxyURL == "" {
		t.Skip("BINANCE_WS_TEST_PROXY is not set")
	}
	baseURL := os.Getenv("BINANCE_WS_TEST_BASE_URL")
	if baseURL == "" {
		baseURL = "wss://fstream.binance.com"
	}
	connector, err := NewSDKConnector(baseURL, proxyURL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	session, err := connector.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := session.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}()
	select {
	case batch := <-session.Events():
		if len(batch) == 0 {
			t.Fatal("received empty mini ticker batch")
		}
		for _, ticker := range batch {
			if err := ticker.Validate(); err != nil {
				t.Fatalf("invalid live ticker %s: %v", ticker.Symbol, err)
			}
		}
	case err := <-session.Errors():
		t.Fatalf("stream error: %v", err)
	case <-ctx.Done():
		t.Fatalf("timed out waiting for Binance mini tickers: %v", ctx.Err())
	}
}
