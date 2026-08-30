package candidatepool

import (
	"context"
	"testing"
	"time"

	"binance-monitor/internal/domain/signal"
)

type recordingRepository struct {
	inputs  []signal.CandidateInput
	members []signal.CandidateMember
	batch   signal.CandidateBatch
}

func (r *recordingRepository) LoadCandidateResult(context.Context, time.Time, string) (signal.CandidateWriteResult, bool, error) {
	return signal.CandidateWriteResult{}, false, nil
}

func (r *recordingRepository) LoadCandidateInputs(context.Context, time.Time, string) ([]signal.CandidateInput, error) {
	return append([]signal.CandidateInput(nil), r.inputs...), nil
}

func (r *recordingRepository) LoadCandidateMembers(context.Context, string) ([]signal.CandidateMember, error) {
	return append([]signal.CandidateMember(nil), r.members...), nil
}

func (r *recordingRepository) SaveCandidateBatch(_ context.Context, batch signal.CandidateBatch) (signal.CandidateWriteResult, error) {
	r.batch = batch
	return summarize(batch), nil
}

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

func TestServiceLoadsCalculatesAndPersists(t *testing.T) {
	asOf := time.Date(2026, 8, 30, 5, 0, 0, 0, time.UTC)
	rules := testRules()
	calculator, _ := NewCalculator(rules)
	repository := &recordingRepository{inputs: []signal.CandidateInput{
		candidateInput("AUSDT", "4", "4"), candidateInput("BUSDT", "0", "0"),
	}}
	service, err := NewService(repository, calculator, fixedClock{now: asOf.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.RunAt(context.Background(), asOf)
	if err != nil {
		t.Fatal(err)
	}
	if result.Evaluated != 2 || result.Active != 1 || result.Entered != 1 ||
		!repository.batch.AsOf.Equal(asOf) || repository.batch.Rules.RuleVersion != rules.RuleVersion {
		t.Fatalf("result=%#v batch=%#v", result, repository.batch)
	}
}

func summarize(batch signal.CandidateBatch) signal.CandidateWriteResult {
	result := signal.CandidateWriteResult{AsOf: batch.AsOf, Evaluated: len(batch.Evaluations)}
	for _, member := range batch.Members {
		if member.Status == signal.CandidateMemberActive {
			result.Active++
		}
	}
	for _, evaluation := range batch.Evaluations {
		switch evaluation.Outcome {
		case signal.CandidateEntered:
			result.Entered++
		case signal.CandidateContinued:
			result.Continued++
		case signal.CandidateMissHeld:
			result.Held++
		case signal.CandidateExited:
			result.Exited++
		case signal.CandidateRejectedQuality:
			result.RejectedQuality++
		case signal.CandidateRejectedMomentum:
			result.RejectedMomentum++
		case signal.CandidateRejectedLiquidity:
			result.RejectedLiquidity++
		case signal.CandidateRejectedCapacity:
			result.RejectedCapacity++
		case signal.CandidateRejectedCooldown:
			result.RejectedCooldown++
		}
	}
	return result
}
