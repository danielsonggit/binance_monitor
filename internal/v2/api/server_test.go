package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type fakeChecker struct {
	err error
}

func (f fakeChecker) Ping(context.Context) error { return f.err }

func TestLiveness(t *testing.T) {
	server := newTestServer(fakeChecker{})
	request := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestReadinessChecksDatabase(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "ready", want: http.StatusOK},
		{name: "database unavailable", err: errors.New("down"), want: http.StatusServiceUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := newTestServer(fakeChecker{err: test.err})
			request := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
		})
	}
}

func newTestServer(checker ReadinessChecker) *Server {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New("127.0.0.1:0", time.Second, checker, logger)
}
