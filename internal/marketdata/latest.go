package marketdata

import (
	"sync"
	"time"

	"binance-monitor/internal/domain/market"
)

// LatestStore is a concurrency-safe in-memory materialized view. It contains no
// durable state and can always be rebuilt from a live stream plus PostgreSQL.
type LatestStore struct {
	mu        sync.RWMutex
	bySymbol  map[string]market.MiniTicker
	updatedAt time.Time
}

func NewLatestStore() *LatestStore {
	return &LatestStore{bySymbol: make(map[string]market.MiniTicker)}
}

func (s *LatestStore) Apply(items []market.MiniTicker) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range items {
		current, exists := s.bySymbol[item.Symbol]
		if exists && item.EventTime.Before(current.EventTime) {
			continue
		}
		s.bySymbol[item.Symbol] = item
		if item.ReceivedAt.After(s.updatedAt) {
			s.updatedAt = item.ReceivedAt
		}
	}
}

func (s *LatestStore) Get(symbol string) (market.MiniTicker, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, exists := s.bySymbol[symbol]
	return item, exists
}

func (s *LatestStore) Snapshot() map[string]market.MiniTicker {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make(map[string]market.MiniTicker, len(s.bySymbol))
	for symbol, item := range s.bySymbol {
		result[symbol] = item
	}
	return result
}

func (s *LatestStore) UpdatedAt() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.updatedAt
}
