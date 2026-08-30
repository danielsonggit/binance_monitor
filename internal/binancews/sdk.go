package binancews

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"binance-monitor/internal/domain/market"

	client "github.com/binance/binance-connector-go/clients/derivativestradingusdsfutures"
	"github.com/binance/binance-connector-go/clients/derivativestradingusdsfutures/src/websocketstreams/models"
	"github.com/binance/binance-connector-go/common/v2/common"
	"github.com/shopspring/decimal"
)

const sdkChannelSize = 32

var ErrConsumerBackpressure = errors.New("Binance WebSocket 消费速度不足")

type SDKConnector struct {
	baseURL        string
	proxyURL       string
	reconnectDelay time.Duration
}

func NewSDKConnector(baseURL, proxyURL string, reconnectDelay time.Duration) (*SDKConnector, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme != "wss" || parsed.Host == "" {
		return nil, fmt.Errorf("无效 BINANCE_WS_BASE_URL %q", baseURL)
	}
	if proxyURL != "" {
		if _, err := makeProxyConfig(proxyURL); err != nil {
			return nil, err
		}
	}
	return &SDKConnector{
		baseURL:        strings.TrimRight(baseURL, "/"),
		proxyURL:       proxyURL,
		reconnectDelay: reconnectDelay,
	}, nil
}

func (c *SDKConnector) Connect(ctx context.Context) (Session, error) {
	options := []common.ConfigurationWebsocketStreamsOption{
		common.WithWsStreamsBasePath(c.baseURL),
		common.WithWsStreamsReconnectDelay(c.reconnectDelay),
		common.WithWsStreamsMode(common.SINGLE),
	}
	if c.proxyURL != "" {
		proxyConfig, err := makeProxyConfig(c.proxyURL)
		if err != nil {
			return nil, err
		}
		options = append(options, common.WithWsStreamsProxy(proxyConfig))
	}
	configuration := common.NewConfigurationWebsocketStreams(options...)
	sdkClient := client.NewBinanceDerivativesTradingUsdsFuturesClient(
		client.WithWebsocketStreams(configuration),
	)
	if err := sdkClient.WebsocketStreams.ConnectMarket([]string{}); err != nil {
		return nil, fmt.Errorf("连接 Binance market stream: %w", err)
	}
	if ctx.Err() != nil {
		_ = sdkClient.WebsocketStreams.WsMarket.CloseWebSocketStreamConnection()
		return nil, ctx.Err()
	}
	handler, err := sdkClient.WebsocketStreams.MarketAPI.AllMarketMiniTickersStream().Execute()
	if err != nil {
		_ = sdkClient.WebsocketStreams.WsMarket.CloseWebSocketStreamConnection()
		return nil, fmt.Errorf("订阅 Binance all-market mini ticker: %w", err)
	}

	session := &sdkSession{
		client:  sdkClient,
		handler: handler,
		events:  make(chan []market.MiniTicker, sdkChannelSize),
		errors:  make(chan error, sdkChannelSize),
		done:    make(chan struct{}),
	}
	handler.On("message", session.onMessage)
	handler.OnError(session.onError)
	return session, nil
}

type sdkSession struct {
	client  *client.BinanceDerivativesTradingUsdsFuturesClient
	handler interface{ Unsubscribe() }
	events  chan []market.MiniTicker
	errors  chan error
	done    chan struct{}
	once    sync.Once
}

func (s *sdkSession) Events() <-chan []market.MiniTicker { return s.events }
func (s *sdkSession) Errors() <-chan error               { return s.errors }

func (s *sdkSession) Close() error {
	var closeErr error
	s.once.Do(func() {
		close(s.done)
		s.handler.Unsubscribe()
		closeErr = s.client.WebsocketStreams.WsMarket.CloseWebSocketStreamConnection()
	})
	return closeErr
}

func (s *sdkSession) onMessage(message models.AllMarketMiniTickersStreamResponse) {
	batch, err := convertMiniTickers(message, time.Now().UTC())
	if len(batch) > 0 {
		select {
		case s.events <- batch:
		case <-s.done:
			return
		default:
			s.onError(ErrConsumerBackpressure)
			return
		}
	}
	if err != nil {
		s.onError(err)
	}
}

func (s *sdkSession) onError(err error) {
	if err == nil {
		return
	}
	select {
	case s.errors <- err:
	case <-s.done:
	default:
	}
}

func convertMiniTickers(
	message models.AllMarketMiniTickersStreamResponse,
	receivedAt time.Time,
) ([]market.MiniTicker, error) {
	result := make([]market.MiniTicker, 0, len(message.Items))
	invalid := 0
	for _, row := range message.Items {
		item, err := convertMiniTicker(row, receivedAt)
		if err != nil {
			invalid++
			continue
		}
		result = append(result, item)
	}
	if invalid > 0 {
		return result, fmt.Errorf("Binance mini ticker batch 包含 %d 条无效记录，总数 %d", invalid, len(message.Items))
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("Binance mini ticker batch 为空")
	}
	return result, nil
}

func convertMiniTicker(
	row models.AllMarketMiniTickersStreamResponseInner,
	receivedAt time.Time,
) (market.MiniTicker, error) {
	lastPrice, err := decimal.NewFromString(row.GetSmallc())
	if err != nil {
		return market.MiniTicker{}, err
	}
	openPrice, err := decimal.NewFromString(row.GetSmallo())
	if err != nil {
		return market.MiniTicker{}, err
	}
	highPrice, err := decimal.NewFromString(row.GetSmallh())
	if err != nil {
		return market.MiniTicker{}, err
	}
	lowPrice, err := decimal.NewFromString(row.GetSmalll())
	if err != nil {
		return market.MiniTicker{}, err
	}
	baseVolume, err := decimal.NewFromString(row.GetSmallv())
	if err != nil {
		return market.MiniTicker{}, err
	}
	quoteVolume, err := decimal.NewFromString(row.GetSmallq())
	if err != nil {
		return market.MiniTicker{}, err
	}
	result := market.MiniTicker{
		Symbol:         strings.ToUpper(row.GetSmalls()),
		EventTime:      time.UnixMilli(row.GetE()).UTC(),
		ReceivedAt:     receivedAt,
		LastPrice:      lastPrice,
		OpenPrice24h:   openPrice,
		HighPrice24h:   highPrice,
		LowPrice24h:    lowPrice,
		BaseVolume24h:  baseVolume,
		QuoteVolume24h: quoteVolume,
	}
	if err := result.Validate(); err != nil {
		return market.MiniTicker{}, err
	}
	return result, nil
}

func makeProxyConfig(raw string) (common.ProxyConfig, error) {
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return common.ProxyConfig{}, fmt.Errorf("无效 WebSocket proxy URL %q", raw)
	}
	port := 80
	if parsed.Scheme == "https" {
		port = 443
	}
	if parsed.Port() != "" {
		port, err = strconv.Atoi(parsed.Port())
		if err != nil {
			return common.ProxyConfig{}, fmt.Errorf("无效 WebSocket proxy port: %w", err)
		}
	}
	result := common.ProxyConfig{
		Host:     parsed.Hostname(),
		Port:     port,
		Protocol: parsed.Scheme,
	}
	if parsed.User != nil {
		result.Auth.Username = parsed.User.Username()
		result.Auth.Password, _ = parsed.User.Password()
	}
	return result, nil
}
