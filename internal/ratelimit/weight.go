package ratelimit

import (
	"context"
	"fmt"

	"golang.org/x/time/rate"
)

// WeightLimiter is a process-scoped weighted token bucket. A single instance
// must be shared by all clients that consume the same upstream IP quota.
type WeightLimiter struct {
	limiter *rate.Limiter
	burst   int
}

func NewWeightLimiter(perMinute, burst int) (*WeightLimiter, error) {
	if perMinute <= 0 {
		return nil, fmt.Errorf("每分钟请求权重必须大于 0")
	}
	if burst <= 0 {
		return nil, fmt.Errorf("请求权重 burst 必须大于 0")
	}
	return &WeightLimiter{
		limiter: rate.NewLimiter(rate.Limit(float64(perMinute)/60), burst),
		burst:   burst,
	}, nil
}

func (l *WeightLimiter) Wait(ctx context.Context, weight int) error {
	if l == nil || l.limiter == nil {
		return fmt.Errorf("请求权重 limiter 不能为空")
	}
	if weight <= 0 {
		return fmt.Errorf("请求权重必须大于 0")
	}
	if weight > l.burst {
		return fmt.Errorf("单次请求权重 %d 超过 burst %d", weight, l.burst)
	}
	if err := l.limiter.WaitN(ctx, weight); err != nil {
		return fmt.Errorf("等待请求权重: %w", err)
	}
	return nil
}
