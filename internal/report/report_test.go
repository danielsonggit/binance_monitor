package report

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"binance-monitor/internal/catalog"
	"binance-monitor/internal/model"
)

func TestTelegramMessagesMatchRequestedShape(t *testing.T) {
	assets, err := catalog.Default()
	if err != nil {
		t.Fatalf("catalog.Default() error = %v", err)
	}
	rankings := model.Rankings{
		TradFiGainers: []model.Mover{mover("KORU", "KORUUSDT", model.BoardTradFi, 19.35, 4.821)},
		TradFiLosers:  []model.Mover{mover("SOXS", "SOXSUSDT", model.BoardTradFi, 49.02, -3.523)},
		CryptoGainers: []model.Mover{mover("EUL", "EULUSDT", model.BoardCrypto, 2.4992, 71.944)},
		CryptoLosers:  []model.Mover{mover("UNKNOWN", "UNKNOWNUSDT", model.BoardCrypto, 0.1, -3.5)},
	}
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	generatedAt := time.Date(2026, 7, 27, 0, 0, 20, 0, location)
	messages := TelegramMessages(rankings, generatedAt, assets, Options{TopN: 5})
	plain := Plain(messages)

	expected := []string{
		"2026-07-27 00:00:20",
		"24h 涨幅榜",
		"TradFi 涨幅前 5",
		"Crypto 涨幅前 5",
		"KORU",
		"EUL",
	}
	for _, value := range expected {
		if !strings.Contains(plain, value) {
			t.Errorf("report does not contain %q\n%s", value, plain)
		}
	}
	notExpected := []string{
		"TradFi 跌幅前 5",
		"Crypto 跌幅前 5",
		"SOXS",
		"UNKNOWN",
		"本地资料库尚无经过核验的项目简介",
	}
	for _, value := range notExpected {
		if strings.Contains(plain, value) {
			t.Errorf("report unexpectedly contains %q\n%s", value, plain)
		}
	}
	for index, message := range messages {
		if utf8.RuneCountInString(message) > maxMessageRunes {
			t.Errorf("message %d has %d runes", index, utf8.RuneCountInString(message))
		}
	}
}

func TestFormatPrice(t *testing.T) {
	tests := map[float64]string{
		1534.79: "1,534.79",
		19.35:   "19.35",
		3:       "3.00",
		0.03019: "0.03019",
	}
	for input, expected := range tests {
		if actual := formatPrice(input); actual != expected {
			t.Errorf("formatPrice(%v) = %q, want %q", input, actual, expected)
		}
	}
}

func mover(
	base string,
	symbol string,
	board model.Board,
	price float64,
	change float64,
) model.Mover {
	return model.Mover{
		Contract: model.Contract{
			BaseAsset: base,
			Symbol:    symbol,
			Board:     board,
		},
		Ticker: model.Ticker{
			LastPrice:     price,
			ChangePercent: change,
		},
	}
}
