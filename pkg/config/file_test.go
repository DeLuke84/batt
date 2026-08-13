package config

import (
	"path/filepath"
	"testing"
	"time"
)

func TestAdapterDisableTimerPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "batt.json")
	configured, err := NewFile(path)
	if err != nil {
		t.Fatal(err)
	}
	until := time.Date(2026, time.July, 21, 12, 30, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	configured.SetAdapterDisableTimer(until)
	if err := configured.Save(); err != nil {
		t.Fatal(err)
	}

	reloaded, err := NewFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.AdapterDisableUntil(); !got.Equal(until) {
		t.Fatalf("AdapterDisableUntil() = %s, want %s", got, until)
	}

	reloaded.ClearAdapterDisableTimer()
	if err := reloaded.Save(); err != nil {
		t.Fatal(err)
	}
	reloadedAgain, err := NewFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloadedAgain.AdapterDisableUntil(); !got.IsZero() {
		t.Fatalf("AdapterDisableUntil() after clear = %s, want zero", got)
	}
}

func TestPreventSleepOnAdapterDisablePersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "batt.json")
	configured, err := NewFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if configured.PreventSleepOnAdapterDisable() {
		t.Fatal("the feature must be off by default")
	}

	configured.SetPreventSleepOnAdapterDisable(true)
	if err := configured.Save(); err != nil {
		t.Fatal(err)
	}

	reloaded, err := NewFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.PreventSleepOnAdapterDisable() {
		t.Fatal("PreventSleepOnAdapterDisable() did not survive a save/load cycle")
	}
}

// The /config endpoint serves NewRawFileConfigFromConfig, not the stored struct.
// A setting missing here is writable but not readable by any client.
func TestRawFileConfigExposesPreventSleepOnAdapterDisable(t *testing.T) {
	// Not NewFileFromConfig(nil, ...): that aliases the shared defaultFileConfig,
	// and the setter below would mutate the process-wide defaults.
	configured := NewFileFromConfig(&RawFileConfig{}, filepath.Join(t.TempDir(), "batt.json"))
	configured.SetPreventSleepOnAdapterDisable(true)

	raw, err := NewRawFileConfigFromConfig(configured)
	if err != nil {
		t.Fatal(err)
	}
	if raw.PreventSleepOnAdapterDisable == nil {
		t.Fatal("field missing from the served config")
	}
	if !*raw.PreventSleepOnAdapterDisable {
		t.Fatal("served config does not reflect the configured value")
	}
}
