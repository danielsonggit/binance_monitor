package model

type Board string

const (
	BoardTradFi Board = "tradfi"
	BoardCrypto Board = "crypto"
)

type Contract struct {
	Symbol             string
	Status             string
	BaseAsset          string
	QuoteAsset         string
	ContractType       string
	UnderlyingType     string
	UnderlyingSubTypes []string
	Board              Board
	PricePrecision     int
	QuantityPrecision  int
	OnboardDateMS      int64
}

type Ticker struct {
	Symbol        string
	LastPrice     float64
	LastPriceRaw  string
	ChangePercent float64
	QuoteVolume   float64
	CloseTimeMS   int64
}

type Mover struct {
	Contract Contract
	Ticker   Ticker
}

type Rankings struct {
	TradFiGainers []Mover
	TradFiLosers  []Mover
	CryptoGainers []Mover
	CryptoLosers  []Mover
}
