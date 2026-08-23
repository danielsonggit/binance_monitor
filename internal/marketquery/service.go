package marketquery

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"binance-monitor/internal/domain/market"
)

var (
	ErrInvalidArgument = errors.New("market query invalid argument")
	ErrNotFound        = errors.New("market query result not found")
)

type Repository interface {
	LatestRanking(context.Context, market.Sector, market.ReturnHorizon, int) (Ranking, error)
	LatestFeature(context.Context, string) (Feature, error)
	LatestQuality(context.Context) (Quality, error)
	SnapshotQuality(context.Context, time.Time) (*SnapshotQuality, error)
}

type Service struct {
	repository Repository
}

func NewService(repository Repository) (*Service, error) {
	if repository == nil {
		return nil, fmt.Errorf("market query repository 不能为空")
	}
	return &Service{repository: repository}, nil
}

func (s *Service) Ranking(
	ctx context.Context,
	sector market.Sector,
	horizon market.ReturnHorizon,
	limit int,
) (Ranking, error) {
	if sector != market.SectorCrypto && sector != market.SectorTradFi {
		return Ranking{}, fmt.Errorf("%w: sector 必须是 CRYPTO 或 TRADFI", ErrInvalidArgument)
	}
	if _, err := horizon.Duration(); err != nil {
		return Ranking{}, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	if limit <= 0 || limit > 100 {
		return Ranking{}, fmt.Errorf("%w: limit 必须在 1 到 100 之间", ErrInvalidArgument)
	}
	return s.repository.LatestRanking(ctx, sector, horizon, limit)
}

func (s *Service) Feature(ctx context.Context, symbol string) (Feature, error) {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	if symbol == "" {
		return Feature{}, fmt.Errorf("%w: symbol 不能为空", ErrInvalidArgument)
	}
	return s.repository.LatestFeature(ctx, symbol)
}

func (s *Service) Quality(ctx context.Context) (Quality, error) {
	return s.repository.LatestQuality(ctx)
}

func (s *Service) SnapshotQuality(ctx context.Context, asOf time.Time) (*SnapshotQuality, error) {
	if !asOf.IsZero() {
		asOf = asOf.UTC()
		if !asOf.Equal(asOf.Truncate(market.SnapshotInterval)) {
			return nil, fmt.Errorf("%w: as_of 必须对齐 5 分钟边界", ErrInvalidArgument)
		}
	}
	result, err := s.repository.SnapshotQuality(ctx, asOf)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, ErrNotFound
	}
	return result, nil
}
