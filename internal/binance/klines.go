package binance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"binance-monitor/internal/domain/market"

	"github.com/shopspring/decimal"
)

const maxKlineLimit = 1500

type KlineRequest struct {
	Symbol    string
	Interval  market.KlineInterval
	StartTime time.Time
	EndTime   time.Time
	Limit     int
}

func (r KlineRequest) Validate() error {
	if strings.TrimSpace(r.Symbol) == "" {
		return fmt.Errorf("K 线请求 symbol 不能为空")
	}
	if _, err := r.Interval.Duration(); err != nil {
		return err
	}
	if !r.StartTime.IsZero() && r.StartTime.UnixMilli() < 0 {
		return fmt.Errorf("K 线请求 start time 不能早于 Unix epoch")
	}
	if !r.EndTime.IsZero() && r.EndTime.UnixMilli() < 0 {
		return fmt.Errorf("K 线请求 end time 不能早于 Unix epoch")
	}
	if !r.StartTime.IsZero() && !r.EndTime.IsZero() && !r.StartTime.Before(r.EndTime) {
		return fmt.Errorf("K 线请求 start time 必须早于 end time")
	}
	if r.Limit < 0 || r.Limit > maxKlineLimit {
		return fmt.Errorf("K 线请求 limit 必须为 0 或 1 到 %d", maxKlineLimit)
	}
	return nil
}

type klineRow []json.RawMessage

func (c *Client) FetchKlines(ctx context.Context, request KlineRequest) ([]market.Kline, error) {
	request.Symbol = strings.ToUpper(strings.TrimSpace(request.Symbol))
	if err := request.Validate(); err != nil {
		return nil, err
	}
	if c == nil || c.http == nil {
		return nil, fmt.Errorf("Binance HTTP client 不能为空")
	}

	query := url.Values{
		"symbol":   []string{request.Symbol},
		"interval": []string{string(request.Interval)},
	}
	if !request.StartTime.IsZero() {
		query.Set("startTime", strconv.FormatInt(request.StartTime.UnixMilli(), 10))
	}
	if !request.EndTime.IsZero() {
		query.Set("endTime", strconv.FormatInt(request.EndTime.UnixMilli(), 10))
	}
	if request.Limit > 0 {
		query.Set("limit", strconv.Itoa(request.Limit))
	}

	var rows []klineRow
	if err := c.http.JSON(
		ctx,
		http.MethodGet,
		c.baseURL+"/fapi/v1/klines",
		query,
		nil,
		&rows,
	); err != nil {
		return nil, fmt.Errorf("读取 Binance K 线 %s: %w", request.Symbol, err)
	}

	klines := make([]market.Kline, 0, len(rows))
	for index, row := range rows {
		kline, err := parseKlineRow(request.Symbol, request.Interval, row)
		if err != nil {
			return nil, fmt.Errorf("解析 Binance K 线 %s 第 %d 行: %w", request.Symbol, index, err)
		}
		klines = append(klines, kline)
	}
	return klines, nil
}

func parseKlineRow(
	symbol string,
	interval market.KlineInterval,
	row klineRow,
) (market.Kline, error) {
	const requiredFields = 11
	if len(row) < requiredFields {
		return market.Kline{}, fmt.Errorf("字段数量为 %d，至少需要 %d", len(row), requiredFields)
	}

	openTimeMS, err := parseKlineInt64(row[0], "open time")
	if err != nil {
		return market.Kline{}, err
	}
	open, err := parseKlineDecimal(row[1], "open")
	if err != nil {
		return market.Kline{}, err
	}
	high, err := parseKlineDecimal(row[2], "high")
	if err != nil {
		return market.Kline{}, err
	}
	low, err := parseKlineDecimal(row[3], "low")
	if err != nil {
		return market.Kline{}, err
	}
	closePrice, err := parseKlineDecimal(row[4], "close")
	if err != nil {
		return market.Kline{}, err
	}
	volume, err := parseKlineDecimal(row[5], "volume")
	if err != nil {
		return market.Kline{}, err
	}
	closeTimeMS, err := parseKlineInt64(row[6], "close time")
	if err != nil {
		return market.Kline{}, err
	}
	quoteVolume, err := parseKlineDecimal(row[7], "quote volume")
	if err != nil {
		return market.Kline{}, err
	}
	tradeCount, err := parseKlineInt64(row[8], "trade count")
	if err != nil {
		return market.Kline{}, err
	}
	takerBuyBaseVolume, err := parseKlineDecimal(row[9], "taker buy base volume")
	if err != nil {
		return market.Kline{}, err
	}
	takerBuyQuoteVolume, err := parseKlineDecimal(row[10], "taker buy quote volume")
	if err != nil {
		return market.Kline{}, err
	}

	kline := market.Kline{
		Symbol:              symbol,
		Interval:            interval,
		OpenTime:            time.UnixMilli(openTimeMS).UTC(),
		CloseTime:           time.UnixMilli(closeTimeMS).UTC(),
		Open:                open,
		High:                high,
		Low:                 low,
		Close:               closePrice,
		Volume:              volume,
		QuoteVolume:         quoteVolume,
		TradeCount:          tradeCount,
		TakerBuyBaseVolume:  takerBuyBaseVolume,
		TakerBuyQuoteVolume: takerBuyQuoteVolume,
	}
	if err := kline.Validate(); err != nil {
		return market.Kline{}, err
	}
	return kline, nil
}

func parseKlineInt64(raw json.RawMessage, field string) (int64, error) {
	var value int64
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, fmt.Errorf("%s 不是有效整数: %w", field, err)
	}
	return value, nil
}

func parseKlineDecimal(raw json.RawMessage, field string) (decimal.Decimal, error) {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return decimal.Decimal{}, fmt.Errorf("%s 不是字符串: %w", field, err)
	}
	parsed, err := decimal.NewFromString(value)
	if err != nil {
		return decimal.Decimal{}, fmt.Errorf("%s 不是有效十进制数: %w", field, err)
	}
	return parsed, nil
}
