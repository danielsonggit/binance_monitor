package market

import (
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

type MiniTicker struct {
	Symbol         string
	EventTime      time.Time
	ReceivedAt     time.Time
	LastPrice      decimal.Decimal
	OpenPrice24h   decimal.Decimal
	HighPrice24h   decimal.Decimal
	LowPrice24h    decimal.Decimal
	BaseVolume24h  decimal.Decimal
	QuoteVolume24h decimal.Decimal
}

func (t MiniTicker) PriceChangePercent24h() decimal.Decimal {
	if !t.OpenPrice24h.IsPositive() {
		return decimal.Zero
	}
	return t.LastPrice.Sub(t.OpenPrice24h).Div(t.OpenPrice24h).Mul(decimal.NewFromInt(100))
}

func (t MiniTicker) Validate() error {
	if strings.TrimSpace(t.Symbol) == "" {
		return fmt.Errorf("mini ticker symbol 不能为空")
	}
	if t.EventTime.IsZero() || t.ReceivedAt.IsZero() {
		return fmt.Errorf("mini ticker %s 缺少事件时间或接收时间", t.Symbol)
	}
	if !t.LastPrice.IsPositive() || !t.OpenPrice24h.IsPositive() {
		return fmt.Errorf("mini ticker %s 价格必须大于 0", t.Symbol)
	}
	if t.HighPrice24h.LessThan(t.LowPrice24h) {
		return fmt.Errorf("mini ticker %s 的 high 小于 low", t.Symbol)
	}
	if t.BaseVolume24h.IsNegative() || t.QuoteVolume24h.IsNegative() {
		return fmt.Errorf("mini ticker %s 的成交量不能为负数", t.Symbol)
	}
	return nil
}
