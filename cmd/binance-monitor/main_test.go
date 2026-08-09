package main

import "testing"

func TestHelpIsNotAnApplicationError(t *testing.T) {
	if err := run([]string{"--help"}); err != nil {
		t.Fatalf("run(--help) error = %v", err)
	}
}

func TestMutuallyExclusiveV1Modes(t *testing.T) {
	if err := run([]string{"--once", "--daemon"}); err == nil {
		t.Fatal("expected mutually exclusive mode error")
	}
}

func TestExplicitV1PrefixUsesLegacyFlags(t *testing.T) {
	if err := run([]string{"v1", "--help"}); err != nil {
		t.Fatalf("run(v1 --help) error = %v", err)
	}
}

func TestV2HelpDoesNotRequireConfiguration(t *testing.T) {
	if err := run([]string{"worker", "--help"}); err != nil {
		t.Fatalf("run(worker --help) error = %v", err)
	}
}

func TestUnknownCommandFails(t *testing.T) {
	if err := run([]string{"unexpected"}); err == nil {
		t.Fatal("expected unknown command error")
	}
}
