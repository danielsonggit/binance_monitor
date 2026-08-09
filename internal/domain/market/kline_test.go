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
