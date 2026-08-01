package main

import (
	"flag"
	"testing"
)

func TestHelpIsNotAnApplicationError(t *testing.T) {
	if err := run([]string{"--help"}); err != nil {
		t.Fatalf("run(--help) error = %v", err)
	}
}

func TestMutuallyExclusiveModes(t *testing.T) {
	if err := run([]string{"--once", "--daemon"}); err == nil {
		t.Fatal("expected mutually exclusive mode error")
	}
}

func TestParseFlagsRejectsPositionals(t *testing.T) {
	if _, err := parseFlags([]string{"unexpected"}); err == nil {
		t.Fatal("expected positional argument error")
	}
}

func TestParseFlagsHelp(t *testing.T) {
	if _, err := parseFlags([]string{"--help"}); err != flag.ErrHelp {
		t.Fatalf("error = %v, want flag.ErrHelp", err)
	}
}
