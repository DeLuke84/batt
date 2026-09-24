//go:build !brew

package main

import (
	"errors"
	"testing"
)

type fakeUninstallHardware struct {
	adapterEnabled bool
	chargeResets   int
	resetErr       error
	enableErr      error
}

func (*fakeUninstallHardware) IsChargingControlCapable() bool { return true }
func (h *fakeUninstallHardware) ResetChargeControl() error {
	h.chargeResets++
	return h.resetErr
}
func (*fakeUninstallHardware) IsAdapterControlCapable() bool { return true }
func (h *fakeUninstallHardware) EnableAdapter() error {
	if h.enableErr != nil {
		return h.enableErr
	}
	h.adapterEnabled = true
	return nil
}

func TestUninstallNoResetStillRestoresAdapter(t *testing.T) {
	hardware := &fakeUninstallHardware{}
	if err := restoreUninstallHardware(hardware, false); err != nil {
		t.Fatal(err)
	}
	if !hardware.adapterEnabled || hardware.chargeResets != 0 {
		t.Fatalf("no-reset must restore wall power without resetting charging: %+v", hardware)
	}

	hardware.enableErr = errors.New("SMC unavailable")
	if err := restoreUninstallHardware(hardware, false); err == nil {
		t.Fatal("adapter failure must stop uninstall before sleep-state recovery")
	}
}

func TestUninstallRestoresAdapterEvenWhenChargeResetFails(t *testing.T) {
	hardware := &fakeUninstallHardware{resetErr: errors.New("charge keys gated")}
	if err := restoreUninstallHardware(hardware, true); err == nil {
		t.Fatal("charge reset failure must be reported")
	}
	if !hardware.adapterEnabled {
		t.Fatal("restore wall power even when charge-limit reset fails")
	}
}
