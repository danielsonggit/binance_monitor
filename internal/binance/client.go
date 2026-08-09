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
	PricePrecision     int      `json:"pricePrecision"`
	QuantityPrecision  int      `json:"quantityPrecision"`
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
	limiter RequestWeightLimiter
}

type RequestWeightLimiter interface {
	Wait(context.Context, int) error
}

const ticker24hAllSymbolsWeight = 40

func New(baseURL string, httpClient *httpjson.Client) *Client {
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), http: httpClient}
}

func NewWithWeightLimiter(
	baseURL string,
	httpClient *httpjson.Client,
	limiter RequestWeightLimiter,
) (*Client, error) {
	if limiter == nil {
		return nil, fmt.Errorf("Binance 请求权重 limiter 不能为空")
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    httpClient,
		limiter: limiter,
	}, nil
}

func (c *Client) FetchMarket(
	ctx context.Context,
	quoteAssets []string,
) (map[string]model.Contract, map[string]model.Ticker, error) {
	contracts, err := c.FetchContracts(ctx, quoteAssets)
	if err != nil {
		return nil, nil, err
	}
	if err := c.waitRequestWeight(ctx, ticker24hAllSymbolsWeight); err != nil {
		return nil, nil, fmt.Errorf("等待 Binance 全市场 24 小时 ticker 请求权重: %w", err)
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
	return contracts, ParseTickers(tickerRows), nil
}

func (c *Client) FetchContracts(
	ctx context.Context,
	quoteAssets []string,
) (map[string]model.Contract, error) {
	if err := c.waitRequestWeight(ctx, 1); err != nil {
		return nil, fmt.Errorf("等待 Binance exchangeInfo 请求权重: %w", err)
	}
	var exchange exchangeInfoResponse
	if err := c.http.JSON(
		ctx,
		http.MethodGet,
		c.baseURL+"/fapi/v1/exchangeInfo",
		nil,
		nil,
		&exchange,
	); err != nil {
		return nil, fmt.Errorf("读取 Binance 合约信息: %w", err)
	}
	return ParseContracts(exchange.Symbols, quoteAssets), nil
}

func (c *Client) waitRequestWeight(ctx context.Context, weight int) error {
	if c == nil {
		return fmt.Errorf("Binance client 不能为空")
	}
	if c.limiter == nil {
		return nil
	}
	return c.limiter.Wait(ctx, weight)
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
			PricePrecision:     row.PricePrecision,
			QuantityPrecision:  row.QuantityPrecision,
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
