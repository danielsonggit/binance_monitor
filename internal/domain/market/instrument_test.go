package market

import "testing"

func TestInstrumentValidate(t *testing.T) {
	valid := Instrument{
		Symbol:       "BTCUSDT",
		BaseAsset:    "BTC",
		QuoteAsset:   "USDT",
		Sector:       SectorCrypto,
		ContractType: "PERPETUAL",
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	invalid := valid
	invalid.Sector = "UNKNOWN"
	if err := invalid.Validate(); err == nil {
		t.Fatal("expected invalid sector error")
	}
}
