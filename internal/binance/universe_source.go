package binance

import (
	"context"
	"sort"

	"binance-monitor/internal/domain/market"
	"binance-monitor/internal/model"
)

// FetchActiveInstruments adapts Binance exchangeInfo into the V2 domain model.
// Only the adapter knows about the legacy Board representation.
func (c *Client) FetchActiveInstruments(
	ctx context.Context,
	quoteAssets []string,
) ([]market.Instrument, error) {
	contracts, err := c.FetchContracts(ctx, quoteAssets)
	if err != nil {
		return nil, err
	}
	result := make([]market.Instrument, 0, len(contracts))
	for _, contract := range contracts {
		sector := market.SectorCrypto
		if contract.Board == model.BoardTradFi {
			sector = market.SectorTradFi
		}
		result = append(result, market.Instrument{
			Symbol:             contract.Symbol,
			BaseAsset:          contract.BaseAsset,
			QuoteAsset:         contract.QuoteAsset,
			Sector:             sector,
			ContractType:       contract.ContractType,
			PricePrecision:     contract.PricePrecision,
			QuantityPrecision:  contract.QuantityPrecision,
			UnderlyingType:     contract.UnderlyingType,
			UnderlyingSubTypes: append([]string(nil), contract.UnderlyingSubTypes...),
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Symbol < result[j].Symbol })
	return result, nil
}
