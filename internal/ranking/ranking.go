package ranking

import (
	"sort"

	"binance-monitor/internal/model"
)

func Build(
	contracts map[string]model.Contract,
	tickers map[string]model.Ticker,
	topN int,
) model.Rankings {
	var tradFi, crypto []model.Mover
	for symbol, contract := range contracts {
		ticker, exists := tickers[symbol]
		if !exists {
			continue
		}
		mover := model.Mover{Contract: contract, Ticker: ticker}
		if contract.Board == model.BoardTradFi {
			tradFi = append(tradFi, mover)
		} else {
			crypto = append(crypto, mover)
		}
	}
	return model.Rankings{
		TradFiGainers: direction(tradFi, topN, true),
		TradFiLosers:  direction(tradFi, topN, false),
		CryptoGainers: direction(crypto, topN, true),
		CryptoLosers:  direction(crypto, topN, false),
	}
}

func direction(rows []model.Mover, topN int, gainers bool) []model.Mover {
	filtered := make([]model.Mover, 0, len(rows))
	for _, row := range rows {
		if (gainers && row.Ticker.ChangePercent > 0) ||
			(!gainers && row.Ticker.ChangePercent < 0) {
			filtered = append(filtered, row)
		}
	}

	sort.Slice(filtered, func(i, j int) bool {
		left, right := filtered[i], filtered[j]
		if left.Ticker.ChangePercent != right.Ticker.ChangePercent {
			if gainers {
				return left.Ticker.ChangePercent > right.Ticker.ChangePercent
			}
			return left.Ticker.ChangePercent < right.Ticker.ChangePercent
		}
		if left.Ticker.QuoteVolume != right.Ticker.QuoteVolume {
			return left.Ticker.QuoteVolume > right.Ticker.QuoteVolume
		}
		return left.Contract.Symbol < right.Contract.Symbol
	})
	if len(filtered) > topN {
		filtered = filtered[:topN]
	}
	return filtered
}
