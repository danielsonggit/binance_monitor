package marketdata

import (
	"sort"
	"sync"
	"time"

	"binance-monitor/internal/domain/market"
)

// WindowStore keeps a bounded, ordered minute-price history per symbol. It is
// deliberately storage-agnostic so the signal engine can be tested without a
// database or a live Binance connection.
type WindowStore struct {
	mu        sync.RWMutex
	retention time.Duration
	bySymbol  map[string][]market.PricePoint
}

func NewWindowStore(retention time.Duration) *WindowStore {
	return &WindowStore{
		retention: retention,
		bySymbol:  make(map[string][]market.PricePoint),
	}
}

func (s *WindowStore) Apply(points []market.PricePoint) {
	if len(points) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, point := range points {
		series := s.bySymbol[point.Symbol]
		index := sort.Search(len(series), func(i int) bool {
			return !series[i].ObservedAt.Before(point.ObservedAt)
		})
		if index < len(series) && series[index].ObservedAt.Equal(point.ObservedAt) {
			series[index] = point
		} else {
			series = append(series, market.PricePoint{})
			copy(series[index+1:], series[index:])
			series[index] = point
		}
		cutoff := series[len(series)-1].ObservedAt.Add(-s.retention)
		first := sort.Search(len(series), func(i int) bool {
			return !series[i].ObservedAt.Before(cutoff)
		})
		s.bySymbol[point.Symbol] = append([]market.PricePoint(nil), series[first:]...)
	}
}

func (s *WindowStore) Prune(cutoff time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for symbol, series := range s.bySymbol {
		first := sort.Search(len(series), func(i int) bool {
			return !series[i].ObservedAt.Before(cutoff)
		})
		if first == len(series) {
			delete(s.bySymbol, symbol)
			continue
		}
		s.bySymbol[symbol] = append([]market.PricePoint(nil), series[first:]...)
	}
}

func (s *WindowStore) Series(symbol string) []market.PricePoint {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]market.PricePoint(nil), s.bySymbol[symbol]...)
}

func (s *WindowStore) Symbols() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.bySymbol)
}
