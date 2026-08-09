package market

import (
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

type KlineInterval string

const (
	KlineInterval15m          KlineInterval = "15m"
	KlineMaxRequestLimit                    = 1500
	KlineSourceBinanceFutures               = "BINANCE_FAPI_REST"
)

func (i KlineInterval) Duration() (time.Duration, error) {
	switch i {
	case KlineInterval15m:
		return 15 * time.Minute, nil
	default:
		return 0, fmt.Errorf("不支持的 K 线周期 %q", i)
	}
}

type Kline struct {
	Symbol              string
	Interval            KlineInterval
	OpenTime            time.Time
	CloseTime           time.Time
	Open                decimal.Decimal
	High                decimal.Decimal
	Low                 decimal.Decimal
	Close               decimal.Decimal
	Volume              decimal.Decimal
	QuoteVolume         decimal.Decimal
	TradeCount          int64
	TakerBuyBaseVolume  decimal.Decimal
	TakerBuyQuoteVolume decimal.Decimal
}

type KlineQuery struct {
	Symbol    string
	Interval  KlineInterval
	StartTime time.Time
	EndTime   time.Time
	Limit     int
}

func (q KlineQuery) Normalized() KlineQuery {
	q.Symbol = strings.ToUpper(strings.TrimSpace(q.Symbol))
	q.StartTime = q.StartTime.UTC()
	q.EndTime = q.EndTime.UTC()
	return q
}

func (q KlineQuery) Validate() error {
	if strings.TrimSpace(q.Symbol) == "" {
		return fmt.Errorf("K 线请求 symbol 不能为空")
	}
	if q.Symbol != strings.ToUpper(q.Symbol) || q.Symbol != strings.TrimSpace(q.Symbol) {
		return fmt.Errorf("K 线请求 symbol 必须是规范化的大写值")
	}
	if _, err := q.Interval.Duration(); err != nil {
		return err
	}
	if !q.StartTime.IsZero() && q.StartTime.UnixMilli() < 0 {
		return fmt.Errorf("K 线请求 start time 不能早于 Unix epoch")
	}
	if !q.EndTime.IsZero() && q.EndTime.UnixMilli() < 0 {
		return fmt.Errorf("K 线请求 end time 不能早于 Unix epoch")
	}
	if !q.StartTime.IsZero() && !q.EndTime.IsZero() && !q.StartTime.Before(q.EndTime) {
		return fmt.Errorf("K 线请求 start time 必须早于 end time")
	}
	if q.Limit < 0 || q.Limit > KlineMaxRequestLimit {
		return fmt.Errorf("K 线请求 limit 必须为 0 或 1 到 %d", KlineMaxRequestLimit)
	}
	return nil
}

type KlineBatch struct {
	Items      []Kline
	Source     string
	ReceivedAt time.Time
}

func (b KlineBatch) Validate() error {
	if strings.TrimSpace(b.Source) == "" {
		return fmt.Errorf("K 线 batch source 不能为空")
	}
	if b.ReceivedAt.IsZero() {
		return fmt.Errorf("K 线 batch received_at 不能为空")
	}
	if len(b.Items) == 0 {
		return fmt.Errorf("K 线 batch 不能为空")
	}
	seen := make(map[string]struct{}, len(b.Items))
	for _, item := range b.Items {
		if err := item.Validate(); err != nil {
			return err
		}
		if !item.IsClosed(b.ReceivedAt) {
			return fmt.Errorf("K 线 %s/%s 尚未完成", item.Symbol, item.OpenTime.UTC().Format(time.RFC3339))
		}
		key := fmt.Sprintf("%s:%d", item.Symbol, item.OpenTime.UnixMilli())
		if _, exists := seen[key]; exists {
			return fmt.Errorf("K 线 batch 存在重复记录 %s", key)
		}
		seen[key] = struct{}{}
	}
	return nil
}

type KlineWriteResult struct {
	Attempted int
	Upserted  int
}

func (k Kline) Validate() error {
	symbol := strings.TrimSpace(k.Symbol)
	if symbol == "" {
		return fmt.Errorf("K 线 symbol 不能为空")
	}
	if symbol != k.Symbol || symbol != strings.ToUpper(symbol) {
		return fmt.Errorf("K 线 symbol 必须是规范化的大写值")
	}

	duration, err := k.Interval.Duration()
	if err != nil {
		return err
	}
	if k.OpenTime.IsZero() || k.CloseTime.IsZero() {
		return fmt.Errorf("K 线 %s 缺少开盘或收盘时间", k.Symbol)
	}
	if k.OpenTime.UnixMilli()%duration.Milliseconds() != 0 {
		return fmt.Errorf("K 线 %s 的 open time 未对齐 %s", k.Symbol, k.Interval)
	}
	wantClose := k.OpenTime.Add(duration).Add(-time.Millisecond)
	if !k.CloseTime.Equal(wantClose) {
		return fmt.Errorf(
			"K 线 %s 的 close time %s 与周期 %s 不匹配，期望 %s",
			k.Symbol,
			k.CloseTime.UTC().Format(time.RFC3339Nano),
			k.Interval,
			wantClose.UTC().Format(time.RFC3339Nano),
		)
	}

	if !k.Open.IsPositive() || !k.High.IsPositive() ||
		!k.Low.IsPositive() || !k.Close.IsPositive() {
		return fmt.Errorf("K 线 %s 的 OHLC 价格必须大于 0", k.Symbol)
	}
	if k.High.LessThan(k.Low) || k.High.LessThan(k.Open) ||
		k.High.LessThan(k.Close) || k.Low.GreaterThan(k.Open) ||
		k.Low.GreaterThan(k.Close) {
		return fmt.Errorf("K 线 %s 的 OHLC 关系无效", k.Symbol)
	}
	if k.Volume.IsNegative() || k.QuoteVolume.IsNegative() ||
		k.TakerBuyBaseVolume.IsNegative() || k.TakerBuyQuoteVolume.IsNegative() {
		return fmt.Errorf("K 线 %s 的成交量不能为负数", k.Symbol)
	}
	if k.TradeCount < 0 {
		return fmt.Errorf("K 线 %s 的成交笔数不能为负数", k.Symbol)
	}
	return nil
}

func (k Kline) IsClosed(at time.Time) bool {
	return !at.Before(k.CloseTime)
}
