package binance

import (
	"context"
	"sort"
	"time"

	"binance-monitor/internal/domain/market"
	"binance-monitor/internal/model"
)

// FetchInstruments adapts the complete recognized Binance exchangeInfo catalog
// into the V2 domain model. Exchange status is retained for point-in-time
// availability classification; it is not silently filtered here.
// Only the adapter knows about the legacy Board representation.
func (c *Client) FetchInstruments(
	ctx context.Context,
	quoteAssets []string,
) ([]market.Instrument, error) {
	contracts, err := c.FetchContractCatalog(ctx, quoteAssets)
	if err != nil {
		return nil, err
	}
	result := make([]market.Instrument, 0, len(contracts))
	for _, contract := range contracts {
		sector := market.SectorCrypto
		if contract.Board == model.BoardTradFi {
			sector = market.SectorTradFi
		}
		var onboardTime time.Time
		if contract.OnboardDateMS > 0 {
			onboardTime = time.UnixMilli(contract.OnboardDateMS).UTC()
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
			OnboardTime:        onboardTime,
			ExchangeStatus:     market.ExchangeStatus(contract.Status),
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Symbol < result[j].Symbol })
	return result, nil
}
