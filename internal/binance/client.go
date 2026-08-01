package binance

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"binance-monitor/internal/httpjson"
	"binance-monitor/internal/model"
)

type exchangeInfoResponse struct {
	Symbols []exchangeSymbol `json:"symbols"`
}

type exchangeSymbol struct {
	Symbol             string   `json:"symbol"`
	BaseAsset          string   `json:"baseAsset"`
	QuoteAsset         string   `json:"quoteAsset"`
	Status             string   `json:"status"`
	ContractType       string   `json:"contractType"`
	UnderlyingType     string   `json:"underlyingType"`
	UnderlyingSubTypes []string `json:"underlyingSubType"`
}

type tickerResponse struct {
	Symbol             string `json:"symbol"`
	LastPrice          string `json:"lastPrice"`
	PriceChangePercent string `json:"priceChangePercent"`
	QuoteVolume        string `json:"quoteVolume"`
	CloseTime          int64  `json:"closeTime"`
}

type Client struct {
	baseURL string
	http    *httpjson.Client
}

func New(baseURL string, httpClient *httpjson.Client) *Client {
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), http: httpClient}
}

func (c *Client) FetchMarket(
	ctx context.Context,
	quoteAssets []string,
) (map[string]model.Contract, map[string]model.Ticker, error) {
	var exchange exchangeInfoResponse
	if err := c.http.JSON(
		ctx,
		http.MethodGet,
		c.baseURL+"/fapi/v1/exchangeInfo",
		nil,
		nil,
		&exchange,
	); err != nil {
		return nil, nil, fmt.Errorf("读取 Binance 合约信息: %w", err)
	}

	var tickerRows []tickerResponse
	if err := c.http.JSON(
		ctx,
		http.MethodGet,
		c.baseURL+"/fapi/v1/ticker/24hr",
		nil,
		nil,
		&tickerRows,
	); err != nil {
		return nil, nil, fmt.Errorf("读取 Binance 24 小时行情: %w", err)
	}
	return ParseContracts(exchange.Symbols, quoteAssets), ParseTickers(tickerRows), nil
}

func ParseContracts(rows []exchangeSymbol, quoteAssets []string) map[string]model.Contract {
	allowedQuotes := make(map[string]struct{}, len(quoteAssets))
	for _, quote := range quoteAssets {
		allowedQuotes[strings.ToUpper(quote)] = struct{}{}
	}

	result := make(map[string]model.Contract)
	for _, row := range rows {
		status := strings.ToUpper(row.Status)
		contractType := strings.ToUpper(row.ContractType)
		quote := strings.ToUpper(row.QuoteAsset)
		if status != "TRADING" {
			continue
		}
		if _, allowed := allowedQuotes[quote]; !allowed {
			continue
		}

		var board model.Board
		switch contractType {
		case "TRADIFI_PERPETUAL":
			board = model.BoardTradFi
		case "PERPETUAL":
			board = model.BoardCrypto
		default:
			continue
		}
		symbol := strings.ToUpper(row.Symbol)
		baseAsset := strings.ToUpper(row.BaseAsset)
		if symbol == "" || baseAsset == "" {
			continue
		}
		result[symbol] = model.Contract{
			Symbol:             symbol,
			BaseAsset:          baseAsset,
			QuoteAsset:         quote,
			ContractType:       contractType,
			UnderlyingType:     strings.ToUpper(row.UnderlyingType),
			UnderlyingSubTypes: append([]string(nil), row.UnderlyingSubTypes...),
			Board:              board,
		}
	}
	return result
}

func ParseTickers(rows []tickerResponse) map[string]model.Ticker {
	result := make(map[string]model.Ticker)
	for _, row := range rows {
		lastPrice, err := strconv.ParseFloat(row.LastPrice, 64)
		if err != nil || lastPrice <= 0 {
			continue
		}
		change, err := strconv.ParseFloat(row.PriceChangePercent, 64)
		if err != nil {
			continue
		}
		volume, err := strconv.ParseFloat(row.QuoteVolume, 64)
		if err != nil {
			volume = 0
		}
		symbol := strings.ToUpper(row.Symbol)
		if symbol == "" {
			continue
		}
		result[symbol] = model.Ticker{
			Symbol:        symbol,
			LastPrice:     lastPrice,
			LastPriceRaw:  row.LastPrice,
			ChangePercent: change,
			QuoteVolume:   volume,
			CloseTimeMS:   row.CloseTime,
		}
	}
	return result
}
