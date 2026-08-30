package signal

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"binance-monitor/internal/domain/market"
	"github.com/shopspring/decimal"
)

const (
	CandidateRuleVersion1 = "candidate-rules-v1"
	DirectionLong         = "LONG"
)

type CandidateMemberStatus string

const (
	CandidateMemberActive   CandidateMemberStatus = "ACTIVE"
	CandidateMemberCooldown CandidateMemberStatus = "COOLDOWN"
)

type CandidateOutcome string

const (
	CandidateEntered           CandidateOutcome = "ENTERED"
	CandidateContinued         CandidateOutcome = "CONTINUED"
	CandidateMissHeld          CandidateOutcome = "MISS_HELD"
	CandidateExited            CandidateOutcome = "EXITED"
	CandidateRejectedQuality   CandidateOutcome = "REJECTED_QUALITY"
	CandidateRejectedMomentum  CandidateOutcome = "REJECTED_MOMENTUM"
	CandidateRejectedLiquidity CandidateOutcome = "REJECTED_LIQUIDITY"
	CandidateRejectedCapacity  CandidateOutcome = "REJECTED_CAPACITY"
	CandidateRejectedCooldown  CandidateOutcome = "REJECTED_COOLDOWN"
)

type CandidateSectorRule struct {
	Sector                   market.Sector   `json:"sector"`
	Return15mFloorPercent    decimal.Decimal `json:"return_15m_floor_percent"`
	Return1hFloorPercent     decimal.Decimal `json:"return_1h_floor_percent"`
	Return15mPercentile      decimal.Decimal `json:"return_15m_percentile"`
	Return1hPercentile       decimal.Decimal `json:"return_1h_percentile"`
	MinimumQuoteVolume1hUSD  decimal.Decimal `json:"minimum_quote_volume_1h_usd"`
	MinimumQuoteVolume24hUSD decimal.Decimal `json:"minimum_quote_volume_24h_usd"`
	Capacity                 int             `json:"capacity"`
}

type CandidateRuleSet struct {
	RuleVersion      string                                `json:"rule_version"`
	FeatureVersion   string                                `json:"feature_version"`
	Direction        string                                `json:"direction"`
	ExitAfterMisses  int                                   `json:"exit_after_misses"`
	Cooldown         time.Duration                         `json:"cooldown"`
	EnforceLiquidity bool                                  `json:"enforce_liquidity"`
	Sectors          map[market.Sector]CandidateSectorRule `json:"sectors"`
}

func CandidateRulesV1() CandidateRuleSet {
	return CandidateRuleSet{
		RuleVersion: CandidateRuleVersion1, FeatureVersion: market.ReturnFeatureVersion1,
		Direction: DirectionLong, ExitAfterMisses: 3, Cooldown: 30 * time.Minute, EnforceLiquidity: true,
		Sectors: map[market.Sector]CandidateSectorRule{
			market.SectorCrypto: {
				Sector: market.SectorCrypto, Return15mFloorPercent: decimal.RequireFromString("1.5"),
				Return1hFloorPercent: decimal.RequireFromString("3"), Return15mPercentile: decimal.RequireFromString("0.95"),
				Return1hPercentile: decimal.RequireFromString("0.90"), MinimumQuoteVolume1hUSD: decimal.NewFromInt(50_000),
				MinimumQuoteVolume24hUSD: decimal.NewFromInt(1_000_000), Capacity: 20,
			},
			market.SectorTradFi: {
				Sector: market.SectorTradFi, Return15mFloorPercent: decimal.RequireFromString("0.5"),
				Return1hFloorPercent: decimal.RequireFromString("1"), Return15mPercentile: decimal.RequireFromString("0.95"),
				Return1hPercentile: decimal.RequireFromString("0.90"), MinimumQuoteVolume1hUSD: decimal.NewFromInt(15_000),
				MinimumQuoteVolume24hUSD: decimal.NewFromInt(500_000), Capacity: 10,
			},
		},
	}
}

func (r CandidateRuleSet) Validate() error {
	if strings.TrimSpace(r.RuleVersion) == "" || strings.TrimSpace(r.FeatureVersion) == "" || r.Direction != DirectionLong {
		return fmt.Errorf("candidate rule 版本、feature 或方向无效")
	}
	if r.ExitAfterMisses <= 0 || r.Cooldown < market.SnapshotInterval || r.Cooldown%market.SnapshotInterval != 0 {
		return fmt.Errorf("candidate rule 退出或冷却参数无效")
	}
	if len(r.Sectors) != 2 {
		return fmt.Errorf("candidate rule 必须包含 Crypto 和 TradFi")
	}
	for _, sector := range []market.Sector{market.SectorCrypto, market.SectorTradFi} {
		rule, ok := r.Sectors[sector]
		if !ok || rule.Sector != sector || rule.Capacity <= 0 ||
			rule.Return15mFloorPercent.IsNegative() || rule.Return1hFloorPercent.IsNegative() ||
			rule.Return15mPercentile.LessThanOrEqual(decimal.Zero) || rule.Return15mPercentile.GreaterThanOrEqual(decimal.NewFromInt(1)) ||
			rule.Return1hPercentile.LessThanOrEqual(decimal.Zero) || rule.Return1hPercentile.GreaterThanOrEqual(decimal.NewFromInt(1)) ||
			rule.MinimumQuoteVolume1hUSD.IsNegative() || rule.MinimumQuoteVolume24hUSD.IsNegative() {
			return fmt.Errorf("candidate sector rule %s 无效", sector)
		}
	}
	return nil
}

func (r CandidateRuleSet) CanonicalJSON() ([]byte, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(r)
}

func (r CandidateRuleSet) Checksum() (string, error) {
	payload, err := r.CanonicalJSON()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

type CandidateInput struct {
	InstrumentID        int64
	Symbol              string
	Sector              market.Sector
	Availability        market.AvailabilityState
	Return15m           decimal.Decimal
	Return1h            decimal.Decimal
	Valid15m            bool
	Valid1h             bool
	RecentQuoteVolume1h decimal.Decimal
	QuoteVolume24h      decimal.Decimal
}

func (i CandidateInput) Validate() error {
	if i.InstrumentID <= 0 || strings.TrimSpace(i.Symbol) == "" || i.Symbol != strings.ToUpper(strings.TrimSpace(i.Symbol)) {
		return fmt.Errorf("candidate input symbol 必须是规范化的大写值")
	}
	if i.Sector != market.SectorCrypto && i.Sector != market.SectorTradFi {
		return fmt.Errorf("candidate input %s sector 无效", i.Symbol)
	}
	if !validAvailability(i.Availability) {
		return fmt.Errorf("candidate input %s availability 无效", i.Symbol)
	}
	if i.RecentQuoteVolume1h.IsNegative() || i.QuoteVolume24h.IsNegative() {
		return fmt.Errorf("candidate input %s 成交额不能为负数", i.Symbol)
	}
	return nil
}

type CandidateMember struct {
	InstrumentID      int64
	Symbol            string
	Sector            market.Sector
	Direction         string
	RuleVersion       string
	Status            CandidateMemberStatus
	EnteredAt         time.Time
	LastSelectedAt    time.Time
	LastEvaluatedAt   time.Time
	ConsecutiveMisses int
	CooldownUntil     time.Time
}

type CandidateEvaluation struct {
	InstrumentID        int64
	Symbol              string
	Sector              market.Sector
	Availability        market.AvailabilityState
	Return15m           decimal.Decimal
	Return1h            decimal.Decimal
	Valid15m            bool
	Valid1h             bool
	Percentile15m       decimal.Decimal
	Percentile1h        decimal.Decimal
	Threshold15m        decimal.Decimal
	Threshold1h         decimal.Decimal
	RecentQuoteVolume1h decimal.Decimal
	QuoteVolume24h      decimal.Decimal
	Trigger15m          bool
	Trigger1h           bool
	LiquidityQualified  bool
	PriorityRatio       decimal.Decimal
	CapacityRank        int
	PriorStatus         CandidateMemberStatus
	Outcome             CandidateOutcome
	ConsecutiveMisses   int
	CooldownUntil       time.Time
	Reasons             []string
}

type CandidateBatch struct {
	AsOf         time.Time
	CalculatedAt time.Time
	Rules        CandidateRuleSet
	Evaluations  []CandidateEvaluation
	Members      []CandidateMember
}

type CandidateWriteResult struct {
	AsOf              time.Time `json:"as_of"`
	Evaluated         int       `json:"evaluated"`
	Active            int       `json:"active"`
	Entered           int       `json:"entered"`
	Continued         int       `json:"continued"`
	Held              int       `json:"held"`
	Exited            int       `json:"exited"`
	RejectedQuality   int       `json:"rejected_quality"`
	RejectedMomentum  int       `json:"rejected_momentum"`
	RejectedLiquidity int       `json:"rejected_liquidity"`
	RejectedCapacity  int       `json:"rejected_capacity"`
	RejectedCooldown  int       `json:"rejected_cooldown"`
	AlreadyApplied    bool      `json:"already_applied"`
}

func (b CandidateBatch) Validate() error {
	if err := b.Rules.Validate(); err != nil {
		return err
	}
	if b.AsOf.IsZero() || !b.AsOf.Equal(b.AsOf.UTC().Truncate(market.SnapshotInterval)) ||
		b.CalculatedAt.IsZero() || b.CalculatedAt.Before(b.AsOf) || len(b.Evaluations) == 0 {
		return fmt.Errorf("candidate batch 时间或 evaluations 无效")
	}
	seenEvaluations := make(map[int64]struct{}, len(b.Evaluations))
	for _, item := range b.Evaluations {
		if item.InstrumentID <= 0 || strings.TrimSpace(item.Symbol) == "" || item.Symbol != strings.ToUpper(strings.TrimSpace(item.Symbol)) ||
			(item.Sector != market.SectorCrypto && item.Sector != market.SectorTradFi) || !validAvailability(item.Availability) ||
			!validCandidateOutcome(item.Outcome) || item.RecentQuoteVolume1h.IsNegative() || item.QuoteVolume24h.IsNegative() ||
			item.Percentile15m.IsNegative() || item.Percentile15m.GreaterThan(decimal.NewFromInt(100)) ||
			item.Percentile1h.IsNegative() || item.Percentile1h.GreaterThan(decimal.NewFromInt(100)) ||
			item.PriorityRatio.IsNegative() || item.CapacityRank < 0 || item.ConsecutiveMisses < 0 || len(item.Reasons) == 0 {
			return fmt.Errorf("candidate evaluation %q 无效", item.Symbol)
		}
		if _, exists := seenEvaluations[item.InstrumentID]; exists {
			return fmt.Errorf("candidate evaluations 存在重复 instrument %d", item.InstrumentID)
		}
		seenEvaluations[item.InstrumentID] = struct{}{}
	}
	seenMembers := make(map[int64]struct{}, len(b.Members))
	for _, member := range b.Members {
		if member.InstrumentID <= 0 || strings.TrimSpace(member.Symbol) == "" || member.Direction != b.Rules.Direction ||
			member.RuleVersion != b.Rules.RuleVersion || member.ConsecutiveMisses < 0 ||
			(member.Sector != market.SectorCrypto && member.Sector != market.SectorTradFi) ||
			(member.Status != CandidateMemberActive && member.Status != CandidateMemberCooldown) ||
			member.EnteredAt.IsZero() || member.LastSelectedAt.Before(member.EnteredAt) ||
			member.LastEvaluatedAt.Before(member.EnteredAt) ||
			(member.Status == CandidateMemberActive && !member.CooldownUntil.IsZero()) ||
			(member.Status == CandidateMemberCooldown && member.CooldownUntil.IsZero()) {
			return fmt.Errorf("candidate member %q 无效", member.Symbol)
		}
		if _, exists := seenEvaluations[member.InstrumentID]; !exists {
			return fmt.Errorf("candidate member %s 缺少同窗口 evaluation", member.Symbol)
		}
		if _, exists := seenMembers[member.InstrumentID]; exists {
			return fmt.Errorf("candidate members 存在重复 instrument %d", member.InstrumentID)
		}
		seenMembers[member.InstrumentID] = struct{}{}
	}
	return nil
}

func validCandidateOutcome(outcome CandidateOutcome) bool {
	switch outcome {
	case CandidateEntered, CandidateContinued, CandidateMissHeld, CandidateExited,
		CandidateRejectedQuality, CandidateRejectedMomentum, CandidateRejectedLiquidity,
		CandidateRejectedCapacity, CandidateRejectedCooldown:
		return true
	default:
		return false
	}
}

func validAvailability(state market.AvailabilityState) bool {
	switch state {
	case market.AvailabilityOpen, market.AvailabilityMarketClosed, market.AvailabilityLowActivity,
		market.AvailabilityDataMissing, market.AvailabilitySourceUnavailable, market.AvailabilityUnknown:
		return true
	default:
		return false
	}
}
