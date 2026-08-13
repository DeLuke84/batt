package daemon

/*
#cgo LDFLAGS: -framework CoreFoundation -framework IOKit

#include <CoreFoundation/CoreFoundation.h>
#include <IOKit/IOReturn.h>

// These three are declared in Apple's IOKitUser sources (IOPMLibPrivate.h) but
// are not part of the public SDK. They are exported by IOKit.framework at
// runtime -- pmset(8) uses them to implement `pmset disablesleep`. Declaring
// them here mirrors what Battery Toolkit does in its IOPMPrivate module
// (BSD-3-Clause, https://github.com/mhaeuser/Battery-Toolkit).
#define kIOPMSleepDisabledKey CFSTR("SleepDisabled")
CFDictionaryRef IOPMCopySystemPowerSettings(void);
IOReturn IOPMSetSystemPowerSetting(CFStringRef key, CFTypeRef value);

// Returns 1 if sleep is disabled, 0 if it is not, -1 on failure.
static int battGetSleepDisabled(void) {
	CFDictionaryRef settings = IOPMCopySystemPowerSettings();
	if (settings == NULL) {
		return -1;
	}

	int result = 0;
	CFTypeRef value = CFDictionaryGetValue(settings, kIOPMSleepDisabledKey);
	if (value != NULL && CFGetTypeID(value) == CFBooleanGetTypeID()) {
		result = CFBooleanGetValue((CFBooleanRef)value) ? 1 : 0;
	}

	CFRelease(settings);
	return result;
}

static IOReturn battSetSleepDisabled(int disabled) {
	return IOPMSetSystemPowerSetting(
		kIOPMSleepDisabledKey,
		disabled ? kCFBooleanTrue : kCFBooleanFalse
	);
}
*/
import "C"

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"github.com/sirupsen/logrus"
)

// SleepDisabled is a global, persistent system power setting. Unlike a power
// assertion it also suppresses lid-close sleep, which is what Clamshell mode
// needs when adapter input is cut: macOS ends Clamshell mode as soon as it
// believes the Mac runs on battery, and the external display sleeps.
//
// Because the setting outlives the process, the value to restore is written to
// disk before the setting is changed and restored on the next daemon start.

// sleepHoldAdapter is the reason id used while adapter input is disabled.
const sleepHoldAdapter = "adapter-disabled"

var (
	sleepDisabledMu sync.Mutex

	// sleepHolds are the reasons currently holding sleep disabled, keyed by
	// reason id. Holds are idempotent per reason rather than counted: batt's
	// call sites are not transition-guarded -- several of them disable adapter
	// input unconditionally (calibration restore, cancel, the /adapter
	// endpoint) -- and a counter would accumulate holds that no matching enable
	// ever releases, leaving the Mac permanently unable to sleep.
	sleepHolds = map[string]bool{}

	// sleepDisabledPrevious is the value to restore once the last hold is gone,
	// i.e. the user's own setting from before batt touched it.
	sleepDisabledPrevious bool

	// sleepDisabledPath holds the pending-restore snapshot. Read and written
	// only with sleepDisabledMu held.
	sleepDisabledPath string

	// Seams for tests.
	getSleepDisabled  = getSleepDisabledSetting
	setSleepDisabled  = setSleepDisabledSetting
	rawDisableAdapter = func() error { return smcConn.DisableAdapter() }
	rawEnableAdapter  = func() error { return smcConn.EnableAdapter() }
)

type sleepDisabledSnapshot struct {
	Previous bool `json:"previous"`
}

func getSleepDisabledSetting() (bool, error) {
	result := C.battGetSleepDisabled()
	if result < 0 {
		return false, fmt.Errorf("IOPMCopySystemPowerSettings returned NULL")
	}
	return result == 1, nil
}

func setSleepDisabledSetting(disabled bool) error {
	var arg C.int
	if disabled {
		arg = 1
	}
	status := C.battSetSleepDisabled(arg)
	if status != C.kIOReturnSuccess {
		return fmt.Errorf("IOPMSetSystemPowerSetting(SleepDisabled=%t) failed: 0x%x", disabled, uint32(status))
	}
	return nil
}

// initSleepDisabledState restores a value left behind by a previous daemon
// instance. Restoring during shutdown is unreliable -- the property service may
// already be torn down -- so batt restores on start instead.
func initSleepDisabledState(path string) {
	sleepDisabledMu.Lock()
	defer sleepDisabledMu.Unlock()

	sleepDisabledPath = path

	snapshot, ok := loadSleepDisabledSnapshotLocked()
	if !ok {
		return
	}

	if err := setSleepDisabled(snapshot.Previous); err != nil {
		// Keep the snapshot: the next start gets another chance.
		logrus.WithError(err).Error("failed to restore SleepDisabled after restart")
		return
	}

	logrus.Infof("restored SleepDisabled=%t left behind by a previous run", snapshot.Previous)
	clearSleepDisabledSnapshotLocked()
}

// loadSleepDisabledSnapshotLocked reads a pending snapshot, if any. A file that
// cannot be parsed is discarded -- keeping it would block every future restore.
func loadSleepDisabledSnapshotLocked() (sleepDisabledSnapshot, bool) {
	var snapshot sleepDisabledSnapshot

	if sleepDisabledPath == "" {
		return snapshot, false
	}

	b, err := os.ReadFile(sleepDisabledPath)
	if err != nil {
		if !os.IsNotExist(err) {
			logrus.WithError(err).Warn("failed to read sleep-disabled state")
		}
		return snapshot, false
	}

	if err := json.Unmarshal(b, &snapshot); err != nil {
		logrus.WithError(err).Warn("discarding malformed sleep-disabled state")
		clearSleepDisabledSnapshotLocked()
		return snapshot, false
	}

	return snapshot, true
}

func persistSleepDisabledSnapshotLocked(previous bool) {
	if sleepDisabledPath == "" {
		return
	}
	b, err := json.Marshal(sleepDisabledSnapshot{Previous: previous})
	if err != nil {
		logrus.WithError(err).Warn("failed to marshal sleep-disabled state")
		return
	}
	if err := os.WriteFile(sleepDisabledPath, b, 0o600); err != nil {
		logrus.WithError(err).Warn("failed to persist sleep-disabled state")
	}
}

func clearSleepDisabledSnapshotLocked() {
	if sleepDisabledPath == "" {
		return
	}
	if err := os.Remove(sleepDisabledPath); err != nil && !os.IsNotExist(err) {
		logrus.WithError(err).Warn("failed to remove sleep-disabled state")
	}
}

// holdSleep suppresses all sleep, including lid-close sleep, on behalf of
// reason. Holding a reason that is already held does nothing.
func holdSleep(reason string) error {
	sleepDisabledMu.Lock()
	defer sleepDisabledMu.Unlock()

	if sleepHolds[reason] {
		return nil
	}

	if len(sleepHolds) == 0 {
		if err := takeFirstHoldLocked(); err != nil {
			return err
		}
	}

	sleepHolds[reason] = true
	return nil
}

// takeFirstHoldLocked records what to restore and disables sleep.
func takeFirstHoldLocked() error {
	// A snapshot on disk means an earlier restore did not complete. Its value is
	// the user's original setting; the live setting is batt's leftover and must
	// not be mistaken for user intent.
	if snapshot, ok := loadSleepDisabledSnapshotLocked(); ok {
		sleepDisabledPrevious = snapshot.Previous
	} else {
		previous, err := getSleepDisabled()
		if err != nil {
			return err
		}
		sleepDisabledPrevious = previous
		persistSleepDisabledSnapshotLocked(previous)
	}

	if sleepDisabledPrevious {
		// The user disabled sleep themselves. Nothing to change, nothing to
		// restore later.
		return nil
	}

	if err := setSleepDisabled(true); err != nil {
		clearSleepDisabledSnapshotLocked()
		return err
	}

	return nil
}

// releaseSleep drops reason's hold. The system setting is restored once the last
// hold is gone. On failure the hold is kept, so ownership is not lost.
func releaseSleep(reason string) error {
	sleepDisabledMu.Lock()
	defer sleepDisabledMu.Unlock()

	if !sleepHolds[reason] {
		return nil
	}

	delete(sleepHolds, reason)
	if len(sleepHolds) > 0 {
		return nil
	}

	if err := restoreSleepLocked(); err != nil {
		sleepHolds[reason] = true
		return err
	}

	return nil
}

// releaseAllSleepHolds drops every hold, for daemon shutdown.
func releaseAllSleepHolds() error {
	sleepDisabledMu.Lock()
	defer sleepDisabledMu.Unlock()

	if len(sleepHolds) == 0 {
		return nil
	}

	sleepHolds = map[string]bool{}
	return restoreSleepLocked()
}

func restoreSleepLocked() error {
	if sleepDisabledPrevious {
		// Sleep was already disabled before batt held it. Leave it alone.
		sleepDisabledPrevious = false
		clearSleepDisabledSnapshotLocked()
		return nil
	}

	if err := setSleepDisabled(false); err != nil {
		// Keep the snapshot so the next daemon start restores it.
		return err
	}

	clearSleepDisabledSnapshotLocked()
	return nil
}

// disableAdapterWithSleepPolicy cuts adapter input. Doing so makes macOS treat
// the Mac as running on battery, which ends Clamshell mode and immediately
// sleeps the external display. With prevent-sleep-on-adapter-disable enabled,
// suppress sleep first so Clamshell mode survives.
//
// Failing to take the hold aborts the operation rather than cutting power
// anyway: the setting exists precisely to keep the display alive, and silently
// proceeding would blank it.
func disableAdapterWithSleepPolicy() error {
	holdRequested := conf != nil && conf.PreventSleepOnAdapterDisable()
	if holdRequested {
		if err := holdSleep(sleepHoldAdapter); err != nil {
			return fmt.Errorf("failed to disable sleep before cutting adapter input: %w", err)
		}
	}

	if err := rawDisableAdapter(); err != nil {
		if holdRequested {
			if releaseErr := releaseSleep(sleepHoldAdapter); releaseErr != nil {
				logrus.WithError(releaseErr).Error("failed to restore sleep after adapter disable failed")
			}
		}
		return err
	}

	return nil
}

// enableAdapterWithSleepPolicy restores adapter input and releases the hold, if
// one is held. The release is unconditional on the setting: the setting may have
// been switched off while the hold was active, and the hold must still go.
func enableAdapterWithSleepPolicy() error {
	if err := rawEnableAdapter(); err != nil {
		return err
	}

	if err := releaseSleep(sleepHoldAdapter); err != nil {
		logrus.WithError(err).Error("failed to restore sleep after enabling adapter")
	}

	return nil
}
