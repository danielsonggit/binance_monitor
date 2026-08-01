package scheduler

import (
	"path/filepath"
	"testing"
	"time"
)

func TestDueSlotObeysGraceAndDeduplication(t *testing.T) {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 27, 4, 5, 0, 0, location)
	slot, due := DueSlot(now, []int{0, 4, 8}, 10, "")
	if !due || slot.Hour() != 4 {
		t.Fatalf("slot = %v, due = %v", slot, due)
	}
	if _, due := DueSlot(now, []int{0, 4, 8}, 10, SlotKey(slot)); due {
		t.Error("duplicate slot should not be due")
	}
	if _, due := DueSlot(now.Add(5*time.Minute), []int{0, 4, 8}, 10, ""); due {
		t.Error("minute 10 is outside grace period")
	}
}

func TestStoreRoundTrip(t *testing.T) {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	slot := time.Date(2026, 7, 27, 8, 0, 0, 0, location)
	store := NewStore(filepath.Join(t.TempDir(), "state", "scheduler.json"))
	last, err := store.LastSuccessfulSlot()
	if err != nil || last != "" {
		t.Fatalf("initial last = %q, error = %v", last, err)
	}
	if err := store.MarkSuccess(slot); err != nil {
		t.Fatalf("MarkSuccess() error = %v", err)
	}
	last, err = store.LastSuccessfulSlot()
	if err != nil {
		t.Fatal(err)
	}
	if last != SlotKey(slot) {
		t.Errorf("last = %q, want %q", last, SlotKey(slot))
	}
}
