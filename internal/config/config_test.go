package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestFromEnvParsesScheduleAndQuotes(t *testing.T) {
	setEnvironment(t, map[string]string{
		"REPORT_TIMEZONE":        "Asia/Shanghai",
		"REPORT_HOURS":           "20,0,4,4,8,12,16",
		"SCHEDULE_GRACE_MINUTES": "10",
		"QUOTE_ASSETS":           "usdt,USDC,usdt",
		"TOP_N":                  "5",
		"HTTP_TIMEOUT_SECONDS":   "20",
		"HTTP_MAX_RETRIES":       "3",
		"STATE_FILE":             "state/test.json",
	})
	settings, err := FromEnv("/tmp/project")
	if err != nil {
		t.Fatalf("FromEnv() error = %v", err)
	}
	if !reflect.DeepEqual(settings.ReportHours, []int{0, 4, 8, 12, 16, 20}) {
		t.Errorf("hours = %v", settings.ReportHours)
	}
	if !reflect.DeepEqual(settings.QuoteAssets, []string{"USDT", "USDC"}) {
		t.Errorf("quotes = %v", settings.QuoteAssets)
	}
	if settings.StateFile != filepath.Join("/tmp/project", "state/test.json") {
		t.Errorf("state = %q", settings.StateFile)
	}
}

func TestFromEnvRejectsHour24(t *testing.T) {
	setEnvironment(t, map[string]string{
		"REPORT_TIMEZONE": "Asia/Shanghai",
		"REPORT_HOURS":    "0,24",
	})
	if _, err := FromEnv("/tmp/project"); err == nil {
		t.Fatal("expected invalid hour error")
	}
}

func TestLoadDotEnvDoesNotOverrideEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("ONE=file\nTWO=\"quoted\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ONE", "environment")
	if err := os.Unsetenv("TWO"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Unsetenv("TWO") })
	if err := LoadDotEnv(path); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("ONE") != "environment" || os.Getenv("TWO") != "quoted" {
		t.Errorf("ONE=%q TWO=%q", os.Getenv("ONE"), os.Getenv("TWO"))
	}
}

func setEnvironment(t *testing.T, values map[string]string) {
	t.Helper()
	for key, value := range values {
		t.Setenv(key, value)
	}
}
