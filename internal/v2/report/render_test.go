package report

import (
	"context"
	"strings"
	"testing"
	"time"

	"binance-monitor/internal/catalog"
	"binance-monitor/internal/domain/market"
	"binance-monitor/internal/marketquery"
	"github.com/shopspring/decimal"
)

func TestRenderContainsEightGainerGroupsAndNoLosers(t *testing.T) {
	asOf := time.Date(2026, 8, 9, 13, 45, 0, 0, time.UTC)
	snapshot := reportSnapshot(asOf)
	assets, err := catalog.FromBytes([]byte(`{
		"BTC":{"name":"Bitcoin","description":"比特币。","url":"https://bitcoin.org"},
		"XAU":{"name":"黄金","description":"黄金参考资产。","url":"https://example.test/xau"}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	location, _ := time.LoadLocation("Asia/Shanghai")
	messages, err := Render(snapshot, assets, location, 5)
	if err != nil {
		t.Fatal(err)
	}
	plain := Plain(messages)
	for _, expected := range []string{
		"2026-08-09 21:45:00", "TradFi 15m 涨幅前 5", "TradFi 24h 涨幅前 5",
		"Crypto 15m 涨幅前 5", "Crypto 24h 涨幅前 5", "BTCUSDT", "XAUUSDT",
		"15m +1.250%", "1h N/A", "数据覆盖：7/8", "上榜标的简介",
	} {
		if !strings.Contains(plain, expected) {
			t.Errorf("missing %q\n%s", expected, plain)
		}
	}
	for _, unexpected := range []string{"跌幅前", "DOWNUSDT"} {
		if strings.Contains(plain, unexpected) {
			t.Errorf("unexpected %q\n%s", unexpected, plain)
		}
	}
	for index, message := range messages {
		if units := telegramUTF16Length(message); units > maxTelegramUTF16Units {
			t.Errorf("message %d length=%d", index, units)
		}
	}
}

func TestPackBlocksUsesUTF16Units(t *testing.T) {
	// Emoji occupy two UTF-16 units each. Two blocks fit by rune count but not
	// by Telegram's actual UTF-16 limit and therefore must be split.
	block := strings.Repeat("😀", 1200)
	messages, err := packBlocks([]string{block, block})
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 {
		t.Fatalf("messages=%d", len(messages))
	}
}

func TestServiceRejectsMixedRankingTimes(t *testing.T) {
	asOf := time.Date(2026, 8, 9, 13, 45, 0, 0, time.UTC)
	source := &reportSourceStub{snapshot: reportSnapshot(asOf), mixed: true}
	assets, _ := catalog.FromBytes([]byte(`{"BTC":{"description":"Bitcoin"}}`))
	service, err := NewService(source, assets, time.UTC, 5)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Snapshot(context.Background()); err == nil {
		t.Fatal("expected mixed time error")
	}
}

type reportSourceStub struct {
	snapshot Snapshot
	mixed    bool
}

func (s *reportSourceStub) Ranking(_ context.Context, sector market.Sector, horizon market.ReturnHorizon, _ int) (marketquery.Ranking, error) {
	for _, group := range s.snapshot.Groups {
		if group.Sector == sector && group.Horizon == horizon {
			if s.mixed && sector == market.SectorCrypto && horizon == market.ReturnHorizon24h {
				group.AsOf = group.AsOf.Add(5 * time.Minute)
			}
			return group, nil
		}
	}
	return marketquery.Ranking{}, marketquery.ErrNotFound
}

func (s *reportSourceStub) Quality(context.Context) (marketquery.Quality, error) {
	return s.snapshot.Quality, nil
}

func reportSnapshot(asOf time.Time) Snapshot {
	valid := decimal.RequireFromString("1.25")
	returns := map[market.ReturnHorizon]marketquery.ReturnMetric{
		market.ReturnHorizon15m: {Horizon: market.ReturnHorizon15m, ReturnPercent: &valid, IsValid: true},
		market.ReturnHorizon1h:  {Horizon: market.ReturnHorizon1h, IsValid: false, InvalidReason: "KLINE_GAPS"},
		market.ReturnHorizon4h:  {Horizon: market.ReturnHorizon4h, ReturnPercent: &valid, IsValid: true},
		market.ReturnHorizon24h: {Horizon: market.ReturnHorizon24h, ReturnPercent: &valid, IsValid: true},
	}
	groups := make([]marketquery.Ranking, 0, 8)
	for _, sector := range []market.Sector{market.SectorTradFi, market.SectorCrypto} {
		for _, horizon := range market.ReturnHorizons() {
			symbol, base := "BTCUSDT", "BTC"
			if sector == market.SectorTradFi {
				symbol, base = "XAUUSDT", "XAU"
			}
			groups = append(groups, marketquery.Ranking{
				AsOf: asOf, Sector: sector, Horizon: horizon,
				ActiveCount: 1, EligibleCount: 1, PositiveCount: 1, RankedCount: 1,
				Items: []marketquery.RankingItem{{
					Rank: 1, Symbol: symbol, BaseAsset: base, Sector: sector,
					CurrentPrice:   decimal.RequireFromString("1234.56"),
					QuoteVolume24h: decimal.RequireFromString("12500000"),
					ReturnPercent:  valid, Percentile: decimal.NewFromInt(100), Returns: returns,
				}},
			})
		}
	}
	return Snapshot{
		AsOf: asOf, Groups: groups,
		Quality: marketquery.Quality{
			AsOf: asOf, ActiveMetrics: 8, ValidMetrics: 7,
			CoveragePercent: decimal.RequireFromString("87.5"),
		},
	}
}
