package candidatepool

import (
	"fmt"
	"sort"
	"time"

	"binance-monitor/internal/domain/market"
	"binance-monitor/internal/domain/signal"
	"github.com/shopspring/decimal"
)

type Calculator struct {
	rules signal.CandidateRuleSet
}

func NewCalculator(rules signal.CandidateRuleSet) (*Calculator, error) {
	if err := rules.Validate(); err != nil {
		return nil, err
	}
	return &Calculator{rules: rules}, nil
}

func (c *Calculator) Calculate(
	asOf time.Time,
	calculatedAt time.Time,
	inputs []signal.CandidateInput,
	members []signal.CandidateMember,
) (signal.CandidateBatch, error) {
	asOf = asOf.UTC()
	calculatedAt = calculatedAt.UTC()
	if asOf.IsZero() || !asOf.Equal(asOf.Truncate(market.SnapshotInterval)) || calculatedAt.IsZero() {
		return signal.CandidateBatch{}, fmt.Errorf("candidate calculation 时间无效")
	}
	inputByID := make(map[int64]signal.CandidateInput, len(inputs))
	for _, input := range inputs {
		if err := input.Validate(); err != nil {
			return signal.CandidateBatch{}, err
		}
		if _, exists := inputByID[input.InstrumentID]; exists {
			return signal.CandidateBatch{}, fmt.Errorf("candidate inputs 存在重复 instrument %d", input.InstrumentID)
		}
		inputByID[input.InstrumentID] = input
	}
	memberByID := make(map[int64]signal.CandidateMember, len(members))
	for _, member := range members {
		if member.InstrumentID <= 0 || member.Symbol == "" || member.RuleVersion != c.rules.RuleVersion || member.Direction != c.rules.Direction ||
			(member.Status != signal.CandidateMemberActive && member.Status != signal.CandidateMemberCooldown) || member.ConsecutiveMisses < 0 {
			return signal.CandidateBatch{}, fmt.Errorf("candidate member %q 无效", member.Symbol)
		}
		if _, exists := memberByID[member.InstrumentID]; exists {
			return signal.CandidateBatch{}, fmt.Errorf("candidate members 存在重复 instrument %d", member.InstrumentID)
		}
		memberByID[member.InstrumentID] = member
		if _, exists := inputByID[member.InstrumentID]; !exists {
			inputByID[member.InstrumentID] = signal.CandidateInput{
				InstrumentID: member.InstrumentID, Symbol: member.Symbol,
				Sector: member.Sector, Availability: market.AvailabilityUnknown,
			}
		}
	}

	orderedInputs := make([]signal.CandidateInput, 0, len(inputByID))
	for _, input := range inputByID {
		orderedInputs = append(orderedInputs, input)
	}
	sort.Slice(orderedInputs, func(i, j int) bool {
		if orderedInputs[i].Sector != orderedInputs[j].Sector {
			return orderedInputs[i].Sector < orderedInputs[j].Sector
		}
		if orderedInputs[i].Symbol != orderedInputs[j].Symbol {
			return orderedInputs[i].Symbol < orderedInputs[j].Symbol
		}
		return orderedInputs[i].InstrumentID < orderedInputs[j].InstrumentID
	})

	thresholds, distributions := c.thresholds(orderedInputs)
	evaluations := make([]signal.CandidateEvaluation, 0, len(orderedInputs))
	evaluationIndex := make(map[int64]int, len(orderedInputs))
	eligible := make(map[int64]bool, len(orderedInputs))
	for _, input := range orderedInputs {
		evaluation := c.evaluateInput(input, thresholds[input.Sector], distributions[input.Sector])
		if member, exists := memberByID[input.InstrumentID]; exists {
			evaluation.PriorStatus = member.Status
		}
		eligible[input.InstrumentID] = evaluation.Outcome == ""
		evaluationIndex[input.InstrumentID] = len(evaluations)
		evaluations = append(evaluations, evaluation)
	}

	nextMembers := make(map[int64]signal.CandidateMember, len(members))
	activeBySector := map[market.Sector]int{market.SectorCrypto: 0, market.SectorTradFi: 0}
	for _, member := range members {
		member.LastEvaluatedAt = asOf
		nextMembers[member.InstrumentID] = member
		if member.Status != signal.CandidateMemberActive {
			continue
		}
		index := evaluationIndex[member.InstrumentID]
		evaluation := &evaluations[index]
		if eligible[member.InstrumentID] {
			evaluation.Outcome = signal.CandidateContinued
			evaluation.Reasons = append(evaluation.Reasons, "ACTIVE_TRIGGER_CONTINUED")
			evaluation.ConsecutiveMisses = 0
			member.ConsecutiveMisses = 0
			member.LastSelectedAt = asOf
			member.CooldownUntil = time.Time{}
			nextMembers[member.InstrumentID] = member
			activeBySector[member.Sector]++
			continue
		}
		member.ConsecutiveMisses++
		evaluation.ConsecutiveMisses = member.ConsecutiveMisses
		if member.ConsecutiveMisses < c.rules.ExitAfterMisses {
			evaluation.Outcome = signal.CandidateMissHeld
			evaluation.Reasons = append(evaluation.Reasons, "EXIT_HYSTERESIS_HELD")
			nextMembers[member.InstrumentID] = member
			activeBySector[member.Sector]++
			continue
		}
		member.Status = signal.CandidateMemberCooldown
		member.CooldownUntil = asOf.Add(c.rules.Cooldown)
		evaluation.Outcome = signal.CandidateExited
		evaluation.CooldownUntil = member.CooldownUntil
		evaluation.Reasons = append(evaluation.Reasons, "CONSECUTIVE_MISS_EXIT", "COOLDOWN_STARTED")
		nextMembers[member.InstrumentID] = member
	}

	queued := map[market.Sector][]int{market.SectorCrypto: {}, market.SectorTradFi: {}}
	for index := range evaluations {
		evaluation := &evaluations[index]
		member, existed := memberByID[evaluation.InstrumentID]
		if existed && member.Status == signal.CandidateMemberActive {
			continue
		}
		if !eligible[evaluation.InstrumentID] {
			continue
		}
		if existed && member.Status == signal.CandidateMemberCooldown && asOf.Before(member.CooldownUntil) {
			evaluation.Outcome = signal.CandidateRejectedCooldown
			evaluation.CooldownUntil = member.CooldownUntil
			evaluation.Reasons = append(evaluation.Reasons, "COOLDOWN_ACTIVE")
			continue
		}
		queued[evaluation.Sector] = append(queued[evaluation.Sector], index)
	}

	for _, sector := range []market.Sector{market.SectorCrypto, market.SectorTradFi} {
		indexes := queued[sector]
		sort.Slice(indexes, func(i, j int) bool {
			left, right := evaluations[indexes[i]], evaluations[indexes[j]]
			leftBoth, rightBoth := left.Trigger15m && left.Trigger1h, right.Trigger15m && right.Trigger1h
			if leftBoth != rightBoth {
				return leftBoth
			}
			if comparison := left.PriorityRatio.Cmp(right.PriorityRatio); comparison != 0 {
				return comparison > 0
			}
			if comparison := left.QuoteVolume24h.Cmp(right.QuoteVolume24h); comparison != 0 {
				return comparison > 0
			}
			return left.Symbol < right.Symbol
		})
		slots := c.rules.Sectors[sector].Capacity - activeBySector[sector]
		if slots < 0 {
			slots = 0
		}
		for position, index := range indexes {
			evaluation := &evaluations[index]
			evaluation.CapacityRank = position + 1
			if position >= slots {
				evaluation.Outcome = signal.CandidateRejectedCapacity
				evaluation.Reasons = append(evaluation.Reasons, "SECTOR_CAPACITY_EXHAUSTED")
				continue
			}
			evaluation.Outcome = signal.CandidateEntered
			evaluation.ConsecutiveMisses = 0
			evaluation.CooldownUntil = time.Time{}
			evaluation.Reasons = append(evaluation.Reasons, "SECTOR_CAPACITY_ADMITTED")
			nextMembers[evaluation.InstrumentID] = signal.CandidateMember{
				InstrumentID: evaluation.InstrumentID, Symbol: evaluation.Symbol,
				Sector: sector, Direction: c.rules.Direction,
				RuleVersion: c.rules.RuleVersion, Status: signal.CandidateMemberActive,
				EnteredAt: asOf, LastSelectedAt: asOf, LastEvaluatedAt: asOf,
			}
		}
	}

	orderedMembers := make([]signal.CandidateMember, 0, len(nextMembers))
	for _, member := range nextMembers {
		orderedMembers = append(orderedMembers, member)
	}
	sort.Slice(orderedMembers, func(i, j int) bool {
		if orderedMembers[i].Symbol != orderedMembers[j].Symbol {
			return orderedMembers[i].Symbol < orderedMembers[j].Symbol
		}
		return orderedMembers[i].InstrumentID < orderedMembers[j].InstrumentID
	})
	return signal.CandidateBatch{
		AsOf: asOf, CalculatedAt: calculatedAt, Rules: c.rules,
		Evaluations: evaluations, Members: orderedMembers,
	}, nil
}

type sectorThresholds struct {
	return15m decimal.Decimal
	return1h  decimal.Decimal
}

type sectorDistributions struct {
	return15m []decimal.Decimal
	return1h  []decimal.Decimal
}

func (c *Calculator) thresholds(inputs []signal.CandidateInput) (map[market.Sector]sectorThresholds, map[market.Sector]sectorDistributions) {
	distributions := map[market.Sector]sectorDistributions{
		market.SectorCrypto: {}, market.SectorTradFi: {},
	}
	for _, input := range inputs {
		if input.Availability != market.AvailabilityOpen {
			continue
		}
		values := distributions[input.Sector]
		if input.Valid15m {
			values.return15m = append(values.return15m, input.Return15m)
		}
		if input.Valid1h {
			values.return1h = append(values.return1h, input.Return1h)
		}
		distributions[input.Sector] = values
	}
	result := make(map[market.Sector]sectorThresholds, 2)
	for _, sector := range []market.Sector{market.SectorCrypto, market.SectorTradFi} {
		values := distributions[sector]
		sortDecimals(values.return15m)
		sortDecimals(values.return1h)
		distributions[sector] = values
		rule := c.rules.Sectors[sector]
		result[sector] = sectorThresholds{
			return15m: maxDecimal(rule.Return15mFloorPercent, quantile(values.return15m, rule.Return15mPercentile)),
			return1h:  maxDecimal(rule.Return1hFloorPercent, quantile(values.return1h, rule.Return1hPercentile)),
		}
	}
	return result, distributions
}

func (c *Calculator) evaluateInput(input signal.CandidateInput, thresholds sectorThresholds, distributions sectorDistributions) signal.CandidateEvaluation {
	rule := c.rules.Sectors[input.Sector]
	evaluation := signal.CandidateEvaluation{
		InstrumentID: input.InstrumentID, Symbol: input.Symbol,
		Sector: input.Sector, Availability: input.Availability,
		Return15m: input.Return15m, Return1h: input.Return1h, Valid15m: input.Valid15m, Valid1h: input.Valid1h,
		Threshold15m: thresholds.return15m, Threshold1h: thresholds.return1h,
		RecentQuoteVolume1h: input.RecentQuoteVolume1h, QuoteVolume24h: input.QuoteVolume24h,
	}
	if input.Valid15m {
		evaluation.Percentile15m = percentileRank(distributions.return15m, input.Return15m)
		evaluation.Trigger15m = input.Return15m.GreaterThanOrEqual(thresholds.return15m)
	}
	if input.Valid1h {
		evaluation.Percentile1h = percentileRank(distributions.return1h, input.Return1h)
		evaluation.Trigger1h = input.Return1h.GreaterThanOrEqual(thresholds.return1h)
	}
	evaluation.LiquidityQualified = input.RecentQuoteVolume1h.GreaterThanOrEqual(rule.MinimumQuoteVolume1hUSD) &&
		input.QuoteVolume24h.GreaterThanOrEqual(rule.MinimumQuoteVolume24hUSD)
	evaluation.PriorityRatio = priorityRatio(evaluation)
	switch {
	case input.Availability != market.AvailabilityOpen:
		evaluation.Outcome = signal.CandidateRejectedQuality
		evaluation.Reasons = append(evaluation.Reasons, "AVAILABILITY_"+string(input.Availability))
	case !input.Valid15m && !input.Valid1h:
		evaluation.Outcome = signal.CandidateRejectedQuality
		evaluation.Reasons = append(evaluation.Reasons, "NO_VALID_MOMENTUM_HORIZON")
	case !evaluation.Trigger15m && !evaluation.Trigger1h:
		evaluation.Outcome = signal.CandidateRejectedMomentum
		evaluation.Reasons = append(evaluation.Reasons, "MOMENTUM_THRESHOLD_NOT_MET")
	case c.rules.EnforceLiquidity && !evaluation.LiquidityQualified:
		evaluation.Outcome = signal.CandidateRejectedLiquidity
		evaluation.Reasons = append(evaluation.Reasons, "LIQUIDITY_THRESHOLD_NOT_MET")
	default:
		evaluation.Reasons = append(evaluation.Reasons, "DISCOVERY_TRIGGERED", "LIQUIDITY_QUALIFIED")
	}
	return evaluation
}

func priorityRatio(evaluation signal.CandidateEvaluation) decimal.Decimal {
	result := decimal.Zero
	if evaluation.Trigger15m && evaluation.Threshold15m.IsPositive() {
		result = evaluation.Return15m.Div(evaluation.Threshold15m)
	}
	if evaluation.Trigger1h && evaluation.Threshold1h.IsPositive() {
		result = maxDecimal(result, evaluation.Return1h.Div(evaluation.Threshold1h))
	}
	return result.Round(12)
}

func quantile(sortedValues []decimal.Decimal, q decimal.Decimal) decimal.Decimal {
	if len(sortedValues) == 0 {
		return decimal.Zero
	}
	if len(sortedValues) == 1 {
		return sortedValues[0]
	}
	position := decimal.NewFromInt(int64(len(sortedValues) - 1)).Mul(q)
	lower := int(position.IntPart())
	upper := lower + 1
	if upper >= len(sortedValues) {
		return sortedValues[len(sortedValues)-1]
	}
	fraction := position.Sub(decimal.NewFromInt(int64(lower)))
	return sortedValues[lower].Add(sortedValues[upper].Sub(sortedValues[lower]).Mul(fraction)).Round(12)
}

func percentileRank(sortedValues []decimal.Decimal, value decimal.Decimal) decimal.Decimal {
	if len(sortedValues) <= 1 {
		return decimal.NewFromInt(100)
	}
	less := sort.Search(len(sortedValues), func(index int) bool { return sortedValues[index].GreaterThanOrEqual(value) })
	return decimal.NewFromInt(int64(less)).Div(decimal.NewFromInt(int64(len(sortedValues) - 1))).Mul(decimal.NewFromInt(100)).Round(6)
}

func sortDecimals(values []decimal.Decimal) {
	sort.Slice(values, func(i, j int) bool { return values[i].LessThan(values[j]) })
}

func maxDecimal(left, right decimal.Decimal) decimal.Decimal {
	if left.GreaterThan(right) {
		return left
	}
	return right
}
