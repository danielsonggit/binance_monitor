package market

import (
	"fmt"
	"strings"
)

type Sector string

const (
	SectorCrypto Sector = "CRYPTO"
	SectorTradFi Sector = "TRADFI"
)

type Instrument struct {
	Symbol             string
	BaseAsset          string
	QuoteAsset         string
	Sector             Sector
	ContractType       string
	PricePrecision     int
	QuantityPrecision  int
	UnderlyingType     string
	UnderlyingSubTypes []string
}

func (i Instrument) Validate() error {
	if strings.TrimSpace(i.Symbol) == "" {
		return fmt.Errorf("instrument symbol 不能为空")
	}
	if strings.TrimSpace(i.BaseAsset) == "" || strings.TrimSpace(i.QuoteAsset) == "" {
		return fmt.Errorf("instrument %s 的 baseAsset/quoteAsset 不能为空", i.Symbol)
	}
	if i.Sector != SectorCrypto && i.Sector != SectorTradFi {
		return fmt.Errorf("instrument %s 的 sector 无效: %q", i.Symbol, i.Sector)
	}
	if strings.TrimSpace(i.ContractType) == "" {
		return fmt.Errorf("instrument %s 的 contractType 不能为空", i.Symbol)
	}
	if i.PricePrecision < 0 || i.QuantityPrecision < 0 {
		return fmt.Errorf("instrument %s 的 precision 不能为负数", i.Symbol)
	}
	return nil
}
