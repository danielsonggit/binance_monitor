package marketdata

import (
	"testing"
	"time"

	"binance-monitor/internal/domain/market"
)

func TestLatestStoreRejectsOutOfOrderTicker(t *testing.T) {
	store := NewLatestStore()
	newer := market.MiniTicker{Symbol: "BTCUSDT", EventTime: time.Unix(2, 0), ReceivedAt: time.Unix(3, 0)}
	older := market.MiniTicker{Symbol: "BTCUSDT", EventTime: time.Unix(1, 0), ReceivedAt: time.Unix(4, 0)}
	store.Apply([]market.MiniTicker{newer})
	store.Apply([]market.MiniTicker{older})
	got, exists := store.Get("BTCUSDT")
	if !exists || !got.EventTime.Equal(newer.EventTime) {
		t.Fatalf("ticker = %#v", got)
	}
	if !store.UpdatedAt().Equal(newer.ReceivedAt) {
		t.Fatalf("UpdatedAt = %s", store.UpdatedAt())
	}
}

func TestLatestStoreSnapshotIsIndependent(t *testing.T) {
	store := NewLatestStore()
	store.Apply([]market.MiniTicker{{Symbol: "BTCUSDT", EventTime: time.Unix(1, 0)}})
	snapshot := store.Snapshot()
	delete(snapshot, "BTCUSDT")
	if _, exists := store.Get("BTCUSDT"); !exists {
		t.Fatal("mutating snapshot changed store")
	}
}
