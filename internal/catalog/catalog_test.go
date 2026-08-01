package catalog

import (
	"strings"
	"testing"

	"binance-monitor/internal/model"
)

func TestDefaultAndConservativeFallback(t *testing.T) {
	assets, err := Default()
	if err != nil {
		t.Fatalf("Default() error = %v", err)
	}
	known := assets.Describe(model.Contract{BaseAsset: "KORU"})
	if !strings.Contains(known.Description, "3 倍") || known.URL == "" {
		t.Errorf("known intro = %#v", known)
	}

	unknown := assets.Describe(model.Contract{
		Symbol:             "UNKNOWNUSDT",
		BaseAsset:          "UNKNOWN",
		Board:              model.BoardCrypto,
		UnderlyingSubTypes: []string{"AI"},
	})
	if !strings.Contains(unknown.Description, "尚无经过核验") {
		t.Errorf("fallback = %s", unknown.Description)
	}
}
