package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"binance-monitor/internal/domain/market"
	"binance-monitor/internal/marketquery"
)

type ReadinessChecker interface {
	Ping(context.Context) error
}

type MarketReader interface {
	Ranking(context.Context, market.Sector, market.ReturnHorizon, int) (marketquery.Ranking, error)
	Feature(context.Context, string) (marketquery.Feature, error)
	Quality(context.Context) (marketquery.Quality, error)
}

type Server struct {
	address         string
	shutdownTimeout time.Duration
	checker         ReadinessChecker
	market          MarketReader
	logger          *slog.Logger
}

func New(
	address string,
	shutdownTimeout time.Duration,
	checker ReadinessChecker,
	marketReader MarketReader,
	logger *slog.Logger,
) *Server {
	return &Server{
		address:         address,
		shutdownTimeout: shutdownTimeout,
		checker:         checker,
		market:          marketReader,
		logger:          logger,
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", func(response http.ResponseWriter, _ *http.Request) {
		writeJSON(response, http.StatusOK, map[string]any{
			"status":    "alive",
			"component": "v2-api",
		})
	})
	mux.HandleFunc("GET /health/ready", func(response http.ResponseWriter, request *http.Request) {
		ctx, cancel := context.WithTimeout(request.Context(), 2*time.Second)
		defer cancel()
		if err := s.checker.Ping(ctx); err != nil {
			writeJSON(response, http.StatusServiceUnavailable, map[string]any{
				"status": "not_ready",
				"reason": "database_unavailable",
			})
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"status": "ready"})
	})
	mux.HandleFunc("GET /api/v2/rankings", s.handleRanking)
	mux.HandleFunc("GET /api/v2/features/{symbol}", s.handleFeature)
	mux.HandleFunc("GET /api/v2/quality", s.handleQuality)
	return mux
}

func (s *Server) handleRanking(response http.ResponseWriter, request *http.Request) {
	sector := market.Sector(strings.ToUpper(strings.TrimSpace(request.URL.Query().Get("sector"))))
	horizon := market.ReturnHorizon(strings.TrimSpace(request.URL.Query().Get("horizon")))
	limit := 5
	if raw := strings.TrimSpace(request.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeAPIError(response, http.StatusBadRequest, "invalid_limit", "limit 必须是整数")
			return
		}
		limit = parsed
	}
	ctx, cancel := context.WithTimeout(request.Context(), 3*time.Second)
	defer cancel()
	result, err := s.market.Ranking(ctx, sector, horizon, limit)
	if err != nil {
		s.handleQueryError(response, "ranking", err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (s *Server) handleFeature(response http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), 3*time.Second)
	defer cancel()
	result, err := s.market.Feature(ctx, request.PathValue("symbol"))
	if err != nil {
		s.handleQueryError(response, "feature", err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (s *Server) handleQuality(response http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), 3*time.Second)
	defer cancel()
	result, err := s.market.Quality(ctx)
	if err != nil {
		s.handleQueryError(response, "quality", err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (s *Server) handleQueryError(response http.ResponseWriter, operation string, err error) {
	if errors.Is(err, marketquery.ErrNotFound) {
		writeAPIError(response, http.StatusNotFound, "not_found", "没有找到对应的已计算数据")
		return
	}
	if errors.Is(err, marketquery.ErrInvalidArgument) {
		writeAPIError(response, http.StatusBadRequest, "invalid_argument", err.Error())
		return
	}
	s.logger.Error("V2 API 查询失败", "operation", operation, "error", err)
	writeAPIError(response, http.StatusInternalServerError, "query_failed", "查询失败")
}

func (s *Server) Run(ctx context.Context) error {
	listener, err := net.Listen("tcp", s.address)
	if err != nil {
		return fmt.Errorf("监听 V2 API %s: %w", s.address, err)
	}
	server := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- server.Serve(listener)
	}()
	s.logger.Info("V2 API 已启动", "address", listener.Addr().String())

	select {
	case err := <-serveErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("运行 V2 API: %w", err)
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), s.shutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			return fmt.Errorf("关闭 V2 API: %w", err)
		}
		if err := <-serveErrors; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("关闭后的 V2 API 错误: %w", err)
		}
		return nil
	}
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func writeAPIError(response http.ResponseWriter, status int, code, message string) {
	writeJSON(response, status, map[string]string{"code": code, "message": message})
}
