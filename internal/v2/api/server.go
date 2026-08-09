package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

type ReadinessChecker interface {
	Ping(context.Context) error
}

type Server struct {
	address         string
	shutdownTimeout time.Duration
	checker         ReadinessChecker
	logger          *slog.Logger
}

func New(address string, shutdownTimeout time.Duration, checker ReadinessChecker, logger *slog.Logger) *Server {
	return &Server{
		address:         address,
		shutdownTimeout: shutdownTimeout,
		checker:         checker,
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
	return mux
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
