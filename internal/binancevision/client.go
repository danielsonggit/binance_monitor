package binancevision

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"binance-monitor/internal/domain/market"
	"github.com/shopspring/decimal"
)

const (
	defaultBaseURL        = "https://data.binance.vision/data"
	maxCompressedBytes    = 4 << 20
	maxUncompressedBytes  = 16 << 20
	archiveRequestTimeout = 30 * time.Second
	archiveMaxAttempts    = 3
	archiveRetryBase      = 250 * time.Millisecond
)

var ErrArchiveNotFound = errors.New("Binance Vision archive 不存在")

type Client struct {
	baseURL string
	http    *http.Client
}

func New(proxyURL string) (*Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if strings.TrimSpace(proxyURL) != "" {
		parsed, err := url.Parse(proxyURL)
		if err != nil {
			return nil, fmt.Errorf("解析 Binance Vision proxy: %w", err)
		}
		transport.Proxy = http.ProxyURL(parsed)
	}
	return NewWithHTTPClient(defaultBaseURL, &http.Client{
		Timeout:   archiveRequestTimeout,
		Transport: transport,
	})
}

func NewWithHTTPClient(baseURL string, httpClient *http.Client) (*Client, error) {
	if httpClient == nil {
		return nil, fmt.Errorf("Binance Vision HTTP client 不能为空")
	}
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("Binance Vision base URL 无效")
	}
	return &Client{baseURL: parsed.String(), http: httpClient}, nil
}

// FetchDailyKlines returns one complete UTC day from Binance's official USD-M
// archive after verifying its published SHA-256 checksum.
func (c *Client) FetchDailyKlines(
	ctx context.Context,
	symbol string,
	interval market.KlineInterval,
	day time.Time,
) ([]market.Kline, error) {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	if symbol == "" {
		return nil, fmt.Errorf("Binance Vision symbol 不能为空")
	}
	if _, err := interval.Duration(); err != nil {
		return nil, err
	}
	day = day.UTC().Truncate(24 * time.Hour)
	fileName := fmt.Sprintf("%s-%s-%s.zip", symbol, interval, day.Format("2006-01-02"))
	archiveURL := c.baseURL + "/" + path.Join(
		"futures", "um", "daily", "klines", symbol, string(interval), fileName,
	)
	checksumBody, err := c.get(ctx, archiveURL+".CHECKSUM", 1024)
	if err != nil {
		return nil, fmt.Errorf("下载 %s checksum: %w", fileName, err)
	}
	wantChecksum, err := parseChecksum(checksumBody)
	if err != nil {
		return nil, fmt.Errorf("解析 %s checksum: %w", fileName, err)
	}
	archive, err := c.get(ctx, archiveURL, maxCompressedBytes)
	if err != nil {
		return nil, fmt.Errorf("下载 %s: %w", fileName, err)
	}
	gotChecksum := sha256.Sum256(archive)
	if !bytes.Equal(gotChecksum[:], wantChecksum) {
		return nil, fmt.Errorf("%s SHA-256 校验失败", fileName)
	}
	items, err := parseArchive(archive, symbol, interval, day)
	if err != nil {
		return nil, fmt.Errorf("解析 %s: %w", fileName, err)
	}
	return items, nil
}

func (c *Client) get(ctx context.Context, endpoint string, limit int64) ([]byte, error) {
	var lastErr error
	for attempt := 1; attempt <= archiveMaxAttempts; attempt++ {
		payload, err := c.getOnce(ctx, endpoint, limit)
		if err == nil {
			return payload, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if !retryableArchiveError(ctx, err) || attempt == archiveMaxAttempts {
			return nil, err
		}
		if err := waitForRetry(ctx, archiveRetryBase*time.Duration(1<<(attempt-1))); err != nil {
			return nil, err
		}
	}
	return nil, lastErr
}

type httpStatusError struct {
	status int
}

func (e httpStatusError) Error() string { return fmt.Sprintf("HTTP %d", e.status) }

func (c *Client) getOnce(ctx context.Context, endpoint string, limit int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	response, err := c.http.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return nil, ErrArchiveNotFound
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, httpStatusError{status: response.StatusCode}
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(payload)) > limit {
		return nil, fmt.Errorf("响应超过 %d bytes", limit)
	}
	return payload, nil
}

func retryableArchiveError(ctx context.Context, err error) bool {
	if ctx.Err() != nil || errors.Is(err, ErrArchiveNotFound) {
		return false
	}
	var statusErr httpStatusError
	if errors.As(err, &statusErr) {
		return statusErr.status == http.StatusTooManyRequests || statusErr.status >= 500
	}
	return true
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func parseChecksum(payload []byte) ([]byte, error) {
	fields := strings.Fields(string(payload))
	if len(fields) == 0 {
		return nil, fmt.Errorf("checksum 为空")
	}
	value, err := hex.DecodeString(fields[0])
	if err != nil || len(value) != sha256.Size {
		return nil, fmt.Errorf("checksum 不是有效 SHA-256")
	}
	return value, nil
}

func parseArchive(
	payload []byte,
	symbol string,
	interval market.KlineInterval,
	day time.Time,
) ([]market.Kline, error) {
	reader, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		return nil, err
	}
	if len(reader.File) != 1 || reader.File[0].FileInfo().IsDir() {
		return nil, fmt.Errorf("ZIP 必须只包含一个 CSV 文件")
	}
	file := reader.File[0]
	if file.UncompressedSize64 > maxUncompressedBytes {
		return nil, fmt.Errorf("解压内容超过 %d bytes", maxUncompressedBytes)
	}
	stream, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	csvReader := csv.NewReader(io.LimitReader(stream, maxUncompressedBytes+1))
	csvReader.FieldsPerRecord = -1
	items := make([]market.Kline, 0, 96)
	seen := make(map[int64]struct{})
	for rowNumber := 1; ; rowNumber++ {
		row, err := csvReader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("CSV 第 %d 行: %w", rowNumber, err)
		}
		if rowNumber == 1 && len(row) > 0 && strings.EqualFold(strings.TrimSpace(row[0]), "open_time") {
			continue
		}
		item, err := parseRow(row, symbol, interval)
		if err != nil {
			return nil, fmt.Errorf("CSV 第 %d 行: %w", rowNumber, err)
		}
		if item.OpenTime.Before(day) || !item.OpenTime.Before(day.Add(24*time.Hour)) {
			return nil, fmt.Errorf("K 线时间 %s 超出归档 UTC 日期", item.OpenTime)
		}
		key := item.OpenTime.UnixMilli()
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("归档存在重复 open time %d", key)
		}
		seen[key] = struct{}{}
		items = append(items, item)
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("归档不包含 K 线")
	}
	return items, nil
}

func parseRow(row []string, symbol string, interval market.KlineInterval) (market.Kline, error) {
	if len(row) < 11 {
		return market.Kline{}, fmt.Errorf("字段数量为 %d，至少需要 11", len(row))
	}
	intValue := func(index int, name string) (int64, error) {
		value, err := strconv.ParseInt(strings.TrimSpace(row[index]), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("%s 无效: %w", name, err)
		}
		return value, nil
	}
	decimalValue := func(index int, name string) (decimal.Decimal, error) {
		value, err := decimal.NewFromString(strings.TrimSpace(row[index]))
		if err != nil {
			return decimal.Decimal{}, fmt.Errorf("%s 无效: %w", name, err)
		}
		return value, nil
	}
	openTime, err := intValue(0, "open time")
	if err != nil {
		return market.Kline{}, err
	}
	open, err := decimalValue(1, "open")
	if err != nil {
		return market.Kline{}, err
	}
	high, err := decimalValue(2, "high")
	if err != nil {
		return market.Kline{}, err
	}
	low, err := decimalValue(3, "low")
	if err != nil {
		return market.Kline{}, err
	}
	closePrice, err := decimalValue(4, "close")
	if err != nil {
		return market.Kline{}, err
	}
	volume, err := decimalValue(5, "volume")
	if err != nil {
		return market.Kline{}, err
	}
	closeTime, err := intValue(6, "close time")
	if err != nil {
		return market.Kline{}, err
	}
	quoteVolume, err := decimalValue(7, "quote volume")
	if err != nil {
		return market.Kline{}, err
	}
	trades, err := intValue(8, "trade count")
	if err != nil {
		return market.Kline{}, err
	}
	takerBase, err := decimalValue(9, "taker base volume")
	if err != nil {
		return market.Kline{}, err
	}
	takerQuote, err := decimalValue(10, "taker quote volume")
	if err != nil {
		return market.Kline{}, err
	}
	item := market.Kline{
		Symbol: symbol, Interval: interval,
		OpenTime: time.UnixMilli(openTime).UTC(), CloseTime: time.UnixMilli(closeTime).UTC(),
		Open: open, High: high, Low: low, Close: closePrice,
		Volume: volume, QuoteVolume: quoteVolume, TradeCount: trades,
		TakerBuyBaseVolume: takerBase, TakerBuyQuoteVolume: takerQuote,
	}
	if err := item.Validate(); err != nil {
		return market.Kline{}, err
	}
	return item, nil
}
