package market

import (
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestKlineValidate(t *testing.T) {
	valid := testKline()
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid Kline.Validate() error = %v", err)
	}

	tests := []struct {
		name    string
		mutate  func(*Kline)
		wantErr string
	}{
		{
			name: "lowercase symbol",
			mutate: func(kline *Kline) {
				kline.Symbol = "btcusdt"
			},
			wantErr: "规范化",
		},
		{
			name: "unsupported interval",
			mutate: func(kline *Kline) {
				kline.Interval = "1h"
			},
			wantErr: "不支持",
		},
		{
			name: "unaligned open time",
			mutate: func(kline *Kline) {
				kline.OpenTime = kline.OpenTime.Add(time.Minute)
				kline.CloseTime = kline.CloseTime.Add(time.Minute)
			},
			wantErr: "未对齐",
		},
		{
			name: "wrong close time",
			mutate: func(kline *Kline) {
				kline.CloseTime = kline.CloseTime.Add(time.Millisecond)
			},
			wantErr: "不匹配",
		},
		{
			name: "invalid price",
			mutate: func(kline *Kline) {
				kline.Close = decimal.Zero
			},
			wantErr: "必须大于 0",
		},
		{
			name: "invalid ohlc",
			mutate: func(kline *Kline) {
				kline.High = decimal.RequireFromString("99")
			},
			wantErr: "OHLC 关系",
		},
		{
			name: "negative volume",
			mutate: func(kline *Kline) {
				kline.Volume = decimal.NewFromInt(-1)
			},
			wantErr: "成交量不能为负数",
		},
		{
			name: "negative trade count",
			mutate: func(kline *Kline) {
				kline.TradeCount = -1
			},
			wantErr: "成交笔数不能为负数",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			test.mutate(&candidate)
			err := candidate.Validate()
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Validate() error = %v, want containing %q", err, test.wantErr)
			}
		})
	}
}

func TestKlineIsClosed(t *testing.T) {
	kline := testKline()
	if kline.IsClosed(kline.CloseTime.Add(-time.Millisecond)) {
		t.Fatal("Kline.IsClosed() = true before close time")
	}
	if !kline.IsClosed(kline.CloseTime) {
		t.Fatal("Kline.IsClosed() = false at close time")
	}
}

func TestKlineQueryNormalizeAndValidate(t *testing.T) {
	query := KlineQuery{
		Symbol:    " btcusdt ",
		Interval:  KlineInterval15m,
		StartTime: time.UnixMilli(1499040000000),
		EndTime:   time.UnixMilli(1499040000000).Add(time.Hour),
		Limit:     500,
	}.Normalized()
	if query.Symbol != "BTCUSDT" || query.StartTime.Location() != time.UTC || query.EndTime.Location() != time.UTC {
		t.Fatalf("normalized query = %#v", query)
	}
	if err := query.Validate(); err != nil {
		t.Fatalf("KlineQuery.Validate() error = %v", err)
	}

	query.Limit = KlineMaxRequestLimit + 1
	if err := query.Validate(); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("KlineQuery.Validate() error = %v", err)
	}
}

func TestKlineBatchValidateRejectsIncompleteAndDuplicateItems(t *testing.T) {
	kline := testKline()
	receivedAt := kline.CloseTime.Add(time.Millisecond)
	valid := KlineBatch{
		Items:      []Kline{kline},
		Source:     KlineSourceBinanceFutures,
		ReceivedAt: receivedAt,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("KlineBatch.Validate() error = %v", err)
	}

	incomplete := valid
	incomplete.ReceivedAt = kline.CloseTime.Add(-time.Millisecond)
	if err := incomplete.Validate(); err == nil || !strings.Contains(err.Error(), "尚未完成") {
		t.Fatalf("incomplete batch error = %v", err)
	}

	duplicate := valid
	duplicate.Items = []Kline{kline, kline}
	if err := duplicate.Validate(); err == nil || !strings.Contains(err.Error(), "重复") {
		t.Fatalf("duplicate batch error = %v", err)
	}
}

func testKline() Kline {
	openTime := time.UnixMilli(1499040000000).UTC()
	return Kline{
		Symbol:              "BTCUSDT",
		Interval:            KlineInterval15m,
		OpenTime:            openTime,
		CloseTime:           openTime.Add(15*time.Minute - time.Millisecond),
		Open:                decimal.RequireFromString("100"),
		High:                decimal.RequireFromString("110"),
		Low:                 decimal.RequireFromString("95"),
		Close:               decimal.RequireFromString("105"),
		Volume:              decimal.RequireFromString("12.5"),
		QuoteVolume:         decimal.RequireFromString("1300"),
		TradeCount:          42,
		TakerBuyBaseVolume:  decimal.RequireFromString("7"),
		TakerBuyQuoteVolume: decimal.RequireFromString("730"),
	}
}
