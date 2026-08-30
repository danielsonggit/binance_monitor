package notification

import (
	"context"
	"errors"
	"testing"
	"time"
)

type repositoryStub struct {
	enqueue     EnqueueRequest
	job         Job
	found       bool
	sending     int
	sent        int
	failed      int
	completed   int
	retried     int
	dead        int
	unknown     int
	nextAttempt time.Time
	ambiguous   bool
}

func (r *repositoryStub) Enqueue(_ context.Context, request EnqueueRequest) (EnqueueResult, error) {
	r.enqueue = request
	return EnqueueResult{OutboxID: 1, Created: true}, nil
}

func TestEnqueuerFreezesUniqueRecipients(t *testing.T) {
	repository := &repositoryStub{}
	enqueuer, err := NewEnqueuer(repository, []string{"-1", "-2", "-1"})
	if err != nil {
		t.Fatal(err)
	}
	slot := time.Date(2026, 8, 10, 0, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	result, err := enqueuer.Enqueue(context.Background(), EnqueueRequest{
		IdempotencyKey: "slot", ScheduledFor: slot, DataAsOf: slot.Add(time.Minute),
		Messages: []string{"one"}, MaxAttempts: 3,
	})
	if err != nil || !result.Created {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if len(repository.enqueue.ChatIDs) != 2 || repository.enqueue.ChatIDs[0] != "-1" || repository.enqueue.ChatIDs[1] != "-2" {
		t.Fatalf("chat IDs=%v", repository.enqueue.ChatIDs)
	}
	if repository.enqueue.ScheduledFor.Location() != time.UTC || repository.enqueue.DataAsOf.Location() != time.UTC {
		t.Fatalf("times were not normalized: %#v", repository.enqueue)
	}
}
func (r *repositoryStub) ClaimDue(context.Context, time.Time) (Job, bool, error) {
	return r.job, r.found, nil
}
func (r *repositoryStub) RecoverStale(context.Context, time.Time) error { return nil }
func (r *repositoryStub) MarkSending(context.Context, int64, string, int) error {
	r.sending++
	return nil
}
func (r *repositoryStub) MarkSent(context.Context, int64, string, int, int64) error {
	r.sent++
	return nil
}
func (r *repositoryStub) MarkFailed(_ context.Context, _ int64, _ string, _ int, _ string, ambiguous bool) error {
	r.failed++
	r.ambiguous = ambiguous
	return nil
}
func (r *repositoryStub) Complete(context.Context, int64, time.Time) error {
	r.completed++
	return nil
}
func (r *repositoryStub) Retry(_ context.Context, _ int64, next time.Time, _ string) error {
	r.retried++
	r.nextAttempt = next
	return nil
}
func (r *repositoryStub) Dead(context.Context, int64, string) error {
	r.dead++
	return nil
}
func (r *repositoryStub) Unknown(context.Context, int64, string) error {
	r.unknown++
	return nil
}

type senderStub struct {
	err   error
	calls int
}

func (s *senderStub) SendTo(context.Context, string, string) (int64, error) {
	s.calls++
	if s.err != nil {
		return 0, s.err
	}
	return int64(100 + s.calls), nil
}

type classifiedError struct {
	ambiguous bool
	retryable bool
}

func (e classifiedError) Error() string   { return "send failed" }
func (e classifiedError) Ambiguous() bool { return e.ambiguous }
func (e classifiedError) Retryable() bool { return e.retryable }

func TestDispatcherCompletesSuccessfulMultipartJob(t *testing.T) {
	repository := &repositoryStub{found: true, job: Job{
		OutboxID: 1, Messages: []string{"one", "two"}, Attempt: 1, MaxAttempts: 3,
		Deliveries: []Delivery{{ChatID: "-1", PartIndex: 0}, {ChatID: "-1", PartIndex: 1}},
	}}
	sender := &senderStub{}
	dispatcher, err := NewDispatcher(repository, sender, time.Second, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	found, err := dispatcher.DispatchOne(context.Background(), time.Now())
	if err != nil || !found || repository.sending != 2 || repository.sent != 2 || repository.completed != 1 {
		t.Fatalf("found=%v err=%v repo=%#v", found, err, repository)
	}
}

func TestDispatcherRetriesDefiniteTransientFailure(t *testing.T) {
	now := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	repository := &repositoryStub{found: true, job: Job{
		OutboxID: 1, Messages: []string{"one"}, Attempt: 2, MaxAttempts: 3,
		Deliveries: []Delivery{{ChatID: "-1", PartIndex: 0}},
	}}
	dispatcher, _ := NewDispatcher(repository, &senderStub{err: classifiedError{retryable: true}}, 10*time.Second, time.Minute)
	if _, err := dispatcher.DispatchOne(context.Background(), now); err == nil {
		t.Fatal("expected send error")
	}
	if repository.failed != 1 || repository.retried != 1 || repository.dead != 0 || !repository.nextAttempt.Equal(now.Add(20*time.Second)) {
		t.Fatalf("repo=%#v", repository)
	}
}

func TestDispatcherDoesNotRetryAmbiguousFailure(t *testing.T) {
	repository := &repositoryStub{found: true, job: Job{
		OutboxID: 1, Messages: []string{"one"}, Attempt: 1, MaxAttempts: 3,
		Deliveries: []Delivery{{ChatID: "-1", PartIndex: 0}},
	}}
	dispatcher, _ := NewDispatcher(repository, &senderStub{err: errors.New("timeout")}, time.Second, time.Minute)
	if _, err := dispatcher.DispatchOne(context.Background(), time.Now()); err == nil {
		t.Fatal("expected send error")
	}
	if repository.failed != 1 || !repository.ambiguous || repository.unknown != 1 || repository.retried != 0 {
		t.Fatalf("repo=%#v", repository)
	}
}
