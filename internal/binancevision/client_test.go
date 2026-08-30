package binancevision

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"binance-monitor/internal/domain/market"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestFetchDailyKlinesVerifiesAndParsesOfficialArchive(t *testing.T) {
	day := time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC)
	archive := testArchive(t, strings.Join([]string{
		"open_time,open,high,low,close,volume,close_time,quote_volume,count,taker_buy_volume,taker_buy_quote_volume,ignore",
		testCSVRow(day, "105"),
		testCSVRow(day.Add(15*time.Minute), "106"),
	}, "\n")+"\n")
	checksum := sha256.Sum256(archive)
	client := testClient(t, func(request *http.Request) (*http.Response, error) {
		payload := archive
		if strings.HasSuffix(request.URL.Path, ".CHECKSUM") {
			payload = []byte(fmt.Sprintf("%x  archive.zip\n", checksum))
		}
		return response(http.StatusOK, payload), nil
	})

	items, err := client.FetchDailyKlines(context.Background(), " btcusdt ", market.KlineInterval15m, day.Add(8*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Symbol != "BTCUSDT" || items[0].Close.String() != "105" ||
		!items[1].OpenTime.Equal(day.Add(15*time.Minute)) {
		t.Fatalf("items = %#v", items)
	}
}

func TestFetchDailyKlinesRejectsChecksumMismatch(t *testing.T) {
	archive := testArchive(t, testCSVRow(time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC), "105"))
	client := testClient(t, func(request *http.Request) (*http.Response, error) {
		if strings.HasSuffix(request.URL.Path, ".CHECKSUM") {
			return response(http.StatusOK, []byte(strings.Repeat("0", 64))), nil
		}
		return response(http.StatusOK, archive), nil
	})
	_, err := client.FetchDailyKlines(context.Background(), "BTCUSDT", market.KlineInterval15m, time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC))
	if err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("error = %v", err)
	}
}

func TestFetchDailyKlinesReturnsTypedNotFound(t *testing.T) {
	client := testClient(t, func(*http.Request) (*http.Response, error) {
		return response(http.StatusNotFound, nil), nil
	})
	_, err := client.FetchDailyKlines(context.Background(), "BTCUSDT", market.KlineInterval15m, time.Now())
	if err == nil || !strings.Contains(err.Error(), ErrArchiveNotFound.Error()) {
		t.Fatalf("error = %v", err)
	}
}

func TestFetchDailyKlinesRetriesTransientArchiveResponse(t *testing.T) {
	day := time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC)
	archive := testArchive(t, testCSVRow(day, "105"))
	checksum := sha256.Sum256(archive)
	var archiveCalls atomic.Int32
	client := testClient(t, func(request *http.Request) (*http.Response, error) {
		if strings.HasSuffix(request.URL.Path, ".CHECKSUM") {
			return response(http.StatusOK, []byte(fmt.Sprintf("%x", checksum))), nil
		}
		if archiveCalls.Add(1) < 3 {
			return response(http.StatusServiceUnavailable, nil), nil
		}
		return response(http.StatusOK, archive), nil
	})
	items, err := client.FetchDailyKlines(context.Background(), "BTCUSDT", market.KlineInterval15m, day)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || archiveCalls.Load() != 3 {
		t.Fatalf("items=%d calls=%d", len(items), archiveCalls.Load())
	}
}

func TestFetchDailyKlinesCancellationStopsRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var calls atomic.Int32
	client := testClient(t, func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		cancel()
		return response(http.StatusServiceUnavailable, nil), nil
	})
	_, err := client.FetchDailyKlines(ctx, "BTCUSDT", market.KlineInterval15m, time.Now())
	if !errors.Is(err, context.Canceled) || calls.Load() != 1 {
		t.Fatalf("error=%v calls=%d", err, calls.Load())
	}
}

func testClient(t *testing.T, transport roundTripFunc) *Client {
	t.Helper()
	client, err := NewWithHTTPClient("https://data.example.test/data", &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func response(status int, payload []byte) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(payload))}
}

func testArchive(t *testing.T, csvBody string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	file, err := writer.Create("BTCUSDT-15m-2026-08-08.csv")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte(csvBody)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func testCSVRow(openTime time.Time, closePrice string) string {
	return fmt.Sprintf(
		"%d,100,110,95,%s,12.5,%d,1300,42,7,730,0",
		openTime.UnixMilli(),
		closePrice,
		openTime.Add(15*time.Minute-time.Millisecond).UnixMilli(),
	)
}
