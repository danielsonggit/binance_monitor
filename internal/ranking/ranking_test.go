package ranking

import (
	"testing"

	"binance-monitor/internal/model"
)

func TestBuildRanksByDirectionAndVolumeTiebreaker(t *testing.T) {
	contracts := map[string]model.Contract{
		"AUSDT": contract("AUSDT", "A", model.BoardTradFi),
		"BUSDT": contract("BUSDT", "B", model.BoardTradFi),
		"CUSDT": contract("CUSDT", "C", model.BoardTradFi),
		"XUSDT": contract("XUSDT", "X", model.BoardCrypto),
		"YUSDT": contract("YUSDT", "Y", model.BoardCrypto),
	}
	tickers := map[string]model.Ticker{
		"AUSDT": ticker("AUSDT", 2, 100),
		"BUSDT": ticker("BUSDT", 2, 200),
		"CUSDT": ticker("CUSDT", -3, 50),
		"XUSDT": ticker("XUSDT", 5, 100),
		"YUSDT": ticker("YUSDT", -4, 100),
	}

	result := Build(contracts, tickers, 5)
	assertSymbols(t, result.TradFiGainers, []string{"BUSDT", "AUSDT"})
	assertSymbols(t, result.TradFiLosers, []string{"CUSDT"})
	assertSymbols(t, result.CryptoGainers, []string{"XUSDT"})
	assertSymbols(t, result.CryptoLosers, []string{"YUSDT"})
}

func contract(symbol, base string, board model.Board) model.Contract {
	return model.Contract{Symbol: symbol, BaseAsset: base, Board: board}
}

func ticker(symbol string, change, volume float64) model.Ticker {
	return model.Ticker{
		Symbol:        symbol,
		LastPrice:     1,
		ChangePercent: change,
		QuoteVolume:   volume,
	}
}

func assertSymbols(t *testing.T, rows []model.Mover, expected []string) {
	t.Helper()
	if len(rows) != len(expected) {
		t.Fatalf("len(rows) = %d, want %d", len(rows), len(expected))
	}
	for index, symbol := range expected {
		if rows[index].Contract.Symbol != symbol {
			t.Errorf("rows[%d] = %s, want %s", index, rows[index].Contract.Symbol, symbol)
		}
	}
}
