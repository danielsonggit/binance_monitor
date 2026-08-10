package report

import (
	"context"
	"fmt"
	"time"

	"binance-monitor/internal/catalog"
	"binance-monitor/internal/domain/market"
	"binance-monitor/internal/marketquery"
)

type Source interface {
	Ranking(context.Context, market.Sector, market.ReturnHorizon, int) (marketquery.Ranking, error)
	Quality(context.Context) (marketquery.Quality, error)
}

type Snapshot struct {
	AsOf    time.Time
	Groups  []marketquery.Ranking
	Quality marketquery.Quality
}

type Service struct {
	source   Source
	catalog  *catalog.Catalog
	location *time.Location
	topN     int
}

func NewService(source Source, assets *catalog.Catalog, location *time.Location, topN int) (*Service, error) {
	if source == nil || assets == nil || location == nil {
		return nil, fmt.Errorf("V2 report 依赖不能为空")
	}
	if topN <= 0 || topN > 100 {
		return nil, fmt.Errorf("V2 report topN 必须在 1 到 100 之间")
	}
	return &Service{source: source, catalog: assets, location: location, topN: topN}, nil
}

func (s *Service) Snapshot(ctx context.Context) (Snapshot, error) {
	sectors := []market.Sector{market.SectorTradFi, market.SectorCrypto}
	groups := make([]marketquery.Ranking, 0, len(sectors)*len(market.ReturnHorizons()))
	var asOf time.Time
	for _, sector := range sectors {
		for _, horizon := range market.ReturnHorizons() {
			group, err := s.source.Ranking(ctx, sector, horizon, s.topN)
			if err != nil {
				return Snapshot{}, fmt.Errorf("加载 %s/%s 榜单: %w", sector, horizon, err)
			}
			if asOf.IsZero() {
				asOf = group.AsOf.UTC()
			} else if !group.AsOf.Equal(asOf) {
				return Snapshot{}, fmt.Errorf("榜单时点不一致：want=%s got=%s/%s=%s", asOf, sector, horizon, group.AsOf)
			}
			groups = append(groups, group)
		}
	}
	quality, err := s.source.Quality(ctx)
	if err != nil {
		return Snapshot{}, fmt.Errorf("加载报告质量摘要: %w", err)
	}
	if !quality.AsOf.Equal(asOf) {
		return Snapshot{}, fmt.Errorf("榜单与质量时点不一致：ranking=%s quality=%s", asOf, quality.AsOf)
	}
	return Snapshot{AsOf: asOf, Groups: groups, Quality: quality}, nil
}

func (s *Service) Messages(ctx context.Context) (Snapshot, []string, error) {
	snapshot, err := s.Snapshot(ctx)
	if err != nil {
		return Snapshot{}, nil, err
	}
	messages, err := Render(snapshot, s.catalog, s.location, s.topN)
	if err != nil {
		return Snapshot{}, nil, err
	}
	return snapshot, messages, nil
}
