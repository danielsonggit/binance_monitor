package ratelimit

import (
	"context"
	"strings"
	"testing"
)

func TestNewWeightLimiterRejectsInvalidConfiguration(t *testing.T) {
	for _, test := range []struct {
		perMinute int
		burst     int
	}{
		{perMinute: 0, burst: 1},
		{perMinute: 1, burst: 0},
	} {
		if _, err := NewWeightLimiter(test.perMinute, test.burst); err == nil {
			t.Fatalf("NewWeightLimiter(%d, %d) error = nil", test.perMinute, test.burst)
		}
	}
}

func TestWeightLimiterHonorsWeightAndCancellation(t *testing.T) {
	limiter, err := NewWeightLimiter(1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := limiter.Wait(context.Background(), 2); err != nil {
		t.Fatalf("initial Wait() error = %v", err)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := limiter.Wait(canceled, 1); err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("canceled Wait() error = %v", err)
	}
	if err := limiter.Wait(context.Background(), 3); err == nil || !strings.Contains(err.Error(), "超过 burst") {
		t.Fatalf("oversized Wait() error = %v", err)
	}
}
