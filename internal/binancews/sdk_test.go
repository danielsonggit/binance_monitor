package binancews

import (
	"testing"
	"time"

	"github.com/binance/binance-connector-go/clients/derivativestradingusdsfutures/src/websocketstreams/models"
)

func TestConvertMiniTickers(t *testing.T) {
	row := models.NewAllMarketMiniTickersStreamResponseInner()
	row.SetSmalle("24hrMiniTicker")
	row.SetE(1785628800123)
	row.SetSmalls("btcusdt")
	row.SetSmallc("65000.12345678")
	row.SetSmallo("64000")
	row.SetSmallh("66000")
	row.SetSmalll("63000")
	row.SetSmallv("123.45")
	row.SetSmallq("8000000.12")
	receivedAt := time.Date(2026, 8, 2, 0, 0, 1, 0, time.UTC)
	items, err := convertMiniTickers(models.AllMarketMiniTickersStreamResponse{Items: []models.AllMarketMiniTickersStreamResponseInner{*row}}, receivedAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Symbol != "BTCUSDT" || items[0].LastPrice.String() != "65000.12345678" {
		t.Fatalf("items = %#v", items)
	}
	if !items[0].ReceivedAt.Equal(receivedAt) {
		t.Fatalf("ReceivedAt = %s", items[0].ReceivedAt)
	}
}

func TestConvertMiniTickersReportsPartialInvalidData(t *testing.T) {
	valid := models.NewAllMarketMiniTickersStreamResponseInner()
	valid.SetE(1785628800123)
	valid.SetSmalls("BTCUSDT")
	valid.SetSmallc("65000")
	valid.SetSmallo("64000")
	valid.SetSmallh("66000")
	valid.SetSmalll("63000")
	valid.SetSmallv("1")
	valid.SetSmallq("1")
	invalid := *valid
	invalid.SetSmalls("BADUSDT")
	invalid.SetSmallc("bad")
	items, err := convertMiniTickers(models.AllMarketMiniTickersStreamResponse{
		Items: []models.AllMarketMiniTickersStreamResponseInner{*valid, invalid},
	}, time.Now())
	if err == nil || len(items) != 1 {
		t.Fatalf("items=%d err=%v", len(items), err)
	}
}

func TestMakeProxyConfig(t *testing.T) {
	config, err := makeProxyConfig("http://user:pass@127.0.0.1:7890")
	if err != nil {
		t.Fatal(err)
	}
	if config.Host != "127.0.0.1" || config.Port != 7890 || config.Auth.Username != "user" || config.Auth.Password != "pass" {
		t.Fatalf("proxy config = %#v", config)
	}
}

func TestNewSDKConnectorValidatesURLs(t *testing.T) {
	if _, err := NewSDKConnector("https://fstream.binance.com", "", time.Second); err == nil {
		t.Fatal("expected websocket URL error")
	}
	if _, err := NewSDKConnector("wss://fstream.binance.com", "socks5://127.0.0.1:7890", time.Second); err == nil {
		t.Fatal("expected proxy URL error")
	}
}
