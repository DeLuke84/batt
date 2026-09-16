package daemon

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/charlie0129/gosmc"

	"github.com/charlie0129/batt/pkg/calibration"
	"github.com/charlie0129/batt/pkg/compatibility"
	"github.com/charlie0129/batt/pkg/smc"
)

// stubBatteryCharge replaces the battery-charge test seam for one test.
func stubBatteryCharge(t *testing.T, charge int) {
	t.Helper()
	previous := smcGetBatteryCharge
	t.Cleanup(func() { smcGetBatteryCharge = previous })
	smcGetBatteryCharge = func() (int, error) { return charge, nil }
}

// useChargeOnceDaemonState installs daemon globals a charge-control request
// needs and restores them afterwards.
func useChargeOnceDaemonState(t *testing.T, configured *mockConf, phase calibration.Phase) {
	t.Helper()
	previousConf, previousCapabilities := conf, capabilities
	previousState, previousStatePath := calibrationState, calibrationStatePath
	t.Cleanup(func() {
		conf, capabilities = previousConf, previousCapabilities
		calibrationState, calibrationStatePath = previousState, previousStatePath
	})

	conf = configured
	capabilities = compatibility.Capabilities{ChargingControl: true}
	calibrationState = &calibration.State{Phase: phase}
	calibrationStatePath = ""
}

func postChargeOnce(path string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, nil)
	response := httptest.NewRecorder()
	setupRoutes().ServeHTTP(response, request)
	return response
}

func TestResolveChargeOnceTarget(t *testing.T) {
	tests := []struct {
		name    string
		upper   int
		full    bool
		want    int
		wantErr error
	}{
		{name: "to the configured limit", upper: 70, want: 70},
		{name: "to full", upper: 70, full: true, want: 100},
		{name: "limit disabled", upper: 100, wantErr: ErrChargeLimitDisabled},
		{name: "limit disabled, full requested", upper: 100, full: true, wantErr: ErrChargeLimitDisabled},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveChargeOnceTarget(&mockConf{upper: tt.upper, lower: tt.upper - 2}, tt.full)
			if err != tt.wantErr {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("target = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestChargeOnceStartedMessage(t *testing.T) {
	if got := chargeOnceStartedMessage(70, 58, 70); !strings.Contains(got, "70%") || !strings.Contains(got, "58%") {
		t.Fatalf("message does not name target and charge: %q", got)
	}
	full := chargeOnceStartedMessage(100, 58, 70)
	if !strings.Contains(full, "100%") || !strings.Contains(full, "70%") {
		t.Fatalf("full message does not name target and restored limit: %q", full)
	}
}

func TestStartChargeOnceToLimitInsideHysteresisGap(t *testing.T) {
	// 58% with a persistent 40-70% band: the charge sits in the gap where batt
	// normally waits, so this is the case the feature exists for.
	configured := &mockConf{upper: 70, lower: 40}
	useChargeOnceDaemonState(t, configured, calibration.PhaseIdle)
	stubBatteryCharge(t, 58)

	response := postChargeOnce("/charge-once/limit")
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusCreated, response.Body.String())
	}
	if configured.chargeOnceTarget != 70 {
		t.Fatalf("chargeOnceTarget = %d, want 70", configured.chargeOnceTarget)
	}
	if configured.upper != 70 || configured.lower != 40 {
		t.Fatalf("configured band changed to %d/%d, want 70/40", configured.upper, configured.lower)
	}
}

func TestStartChargeOnceToFullKeepsConfiguredLimit(t *testing.T) {
	configured := &mockConf{upper: 70, lower: 68}
	useChargeOnceDaemonState(t, configured, calibration.PhaseIdle)
	stubBatteryCharge(t, 70)

	response := postChargeOnce("/charge-once/full")
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusCreated, response.Body.String())
	}
	if configured.chargeOnceTarget != 100 {
		t.Fatalf("chargeOnceTarget = %d, want 100", configured.chargeOnceTarget)
	}
	// Unlike "batt disable --for", a one-time charge never writes limit=100, so
	// there is no saved limit that a crash could strand.
	if configured.upper != 70 || !configured.disableUntil.IsZero() {
		t.Fatalf("configured limit changed: %+v", configured)
	}
}

func TestStartChargeOnceRejections(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		conf    mockConf
		phase   calibration.Phase
		charge  int
		wantErr string
	}{
		{
			name:    "already at the target",
			path:    "/charge-once/limit",
			conf:    mockConf{upper: 70, lower: 40},
			charge:  70,
			wantErr: "already at 70%",
		},
		{
			name:    "already full",
			path:    "/charge-once/full",
			conf:    mockConf{upper: 70, lower: 68},
			charge:  100,
			wantErr: "already at 100%",
		},
		{
			name:    "charge limit disabled",
			path:    "/charge-once/full",
			conf:    mockConf{upper: 100, lower: 98},
			charge:  50,
			wantErr: "batt is not limiting charging",
		},
		{
			name:    "calibration owns the charge limit",
			path:    "/charge-once/limit",
			conf:    mockConf{upper: 70, lower: 40},
			phase:   calibration.PhaseCharge,
			charge:  58,
			wantErr: ErrCalibrationControlsChargeLimit.Error(),
		},
		{
			name:    "temporary disable is pending",
			path:    "/charge-once/full",
			conf:    mockConf{upper: 100, lower: 68, disableUntil: time.Now().Add(time.Hour), preDisableLimit: 70},
			charge:  58,
			wantErr: ErrTemporaryDisableInProgress.Error(),
		},
		{
			name:    "one-time charge already running",
			path:    "/charge-once/full",
			conf:    mockConf{upper: 70, lower: 68, chargeOnceTarget: 70},
			charge:  58,
			wantErr: ErrChargeOnceInProgress.Error(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			phase := tt.phase
			if phase == "" {
				phase = calibration.PhaseIdle
			}
			configured := tt.conf
			before := configured
			useChargeOnceDaemonState(t, &configured, phase)
			stubBatteryCharge(t, tt.charge)

			response := postChargeOnce(tt.path)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusBadRequest, response.Body.String())
			}
			if !strings.Contains(response.Body.String(), tt.wantErr) {
				t.Fatalf("response does not explain the rejection: %s", response.Body.String())
			}
			if configured != before {
				t.Fatalf("rejected request mutated config: %+v, want %+v", configured, before)
			}
		})
	}
}

func TestCancelChargeOnceRestoresConfiguredBehavior(t *testing.T) {
	configured := &mockConf{upper: 70, lower: 40, chargeOnceTarget: 100}
	useChargeOnceDaemonState(t, configured, calibration.PhaseIdle)

	response := postChargeOnce("/charge-once/cancel")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusOK, response.Body.String())
	}
	if configured.chargeOnceTarget != 0 {
		t.Fatalf("chargeOnceTarget = %d, want 0", configured.chargeOnceTarget)
	}
	if configured.upper != 70 || configured.lower != 40 {
		t.Fatalf("configured band changed to %d/%d, want 70/40", configured.upper, configured.lower)
	}
}

func TestCancelChargeOnceWithoutSession(t *testing.T) {
	useChargeOnceDaemonState(t, &mockConf{upper: 70, lower: 40}, calibration.PhaseIdle)

	response := postChargeOnce("/charge-once/cancel")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), ErrChargeOnceNotRunning.Error()) {
		t.Fatalf("response does not explain the rejection: %s", response.Body.String())
	}
}

func TestSetLimitCancelsChargeOnce(t *testing.T) {
	configured := &mockConf{upper: 70, lower: 40, chargeOnceTarget: 100}
	useChargeOnceDaemonState(t, configured, calibration.PhaseIdle)
	stubBatteryCharge(t, 58)
	previousSMC := smcConn
	t.Cleanup(func() { smcConn = previousSMC })
	smcConn = smc.NewMockValues()

	request := httptest.NewRequest(http.MethodPut, "/limit", strings.NewReader("60"))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	setupRoutes().ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusCreated, response.Body.String())
	}
	if configured.chargeOnceTarget != 0 {
		t.Fatalf("chargeOnceTarget = %d, want 0 after an explicit limit change", configured.chargeOnceTarget)
	}
	if configured.upper != 60 {
		t.Fatalf("upper = %d, want 60", configured.upper)
	}
}

func TestStartCalibrationRejectsChargeOnce(t *testing.T) {
	configured := &mockConf{upper: 70, lower: 40, chargeOnceTarget: 100}
	useChargeOnceDaemonState(t, configured, calibration.PhaseIdle)
	capabilities = compatibility.Capabilities{ChargingControl: true, Calibration: true}
	stubCalibrationSleep(t)

	response := postChargeOnce("/calibration/start")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), ErrChargeOnceInProgress.Error()) {
		t.Fatalf("response does not explain the conflict: %s", response.Body.String())
	}
	if calibrationState.Phase != calibration.PhaseIdle {
		t.Fatalf("phase = %s, want idle", calibrationState.Phase)
	}
}

func TestCompleteChargeOnce(t *testing.T) {
	tests := []struct {
		name     string
		target   int
		charge   int
		phase    calibration.Phase
		want     bool
		wantLeft int
	}{
		{name: "no one-time charge", charge: 58, want: false},
		{name: "below the target", target: 70, charge: 58, want: false, wantLeft: 70},
		{name: "reached the target", target: 70, charge: 70, want: true},
		{name: "overshot the target", target: 100, charge: 100, want: true},
		{
			// A persisted conflict can be loaded after a restart. Calibration
			// writes the charge limit itself, so it goes first.
			name:     "calibration owns the charge limit",
			target:   70,
			charge:   70,
			phase:    calibration.PhaseCharge,
			want:     false,
			wantLeft: 70,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			phase := tt.phase
			if phase == "" {
				phase = calibration.PhaseIdle
			}
			previousState, previousCapabilities := calibrationState, capabilities
			t.Cleanup(func() { calibrationState, capabilities = previousState, previousCapabilities })
			calibrationState = &calibration.State{Phase: phase}
			// The completion charge depends on the backend, so name it.
			capabilities = compatibility.Capabilities{
				ChargingControl:   true,
				ChargeControlMode: compatibility.ChargeControlLegacy,
			}
			stubBatteryCharge(t, tt.charge)

			configured := &mockConf{upper: 70, lower: 40, chargeOnceTarget: tt.target}
			if got := completeChargeOnce(configured); got != tt.want {
				t.Fatalf("completeChargeOnce() = %v, want %v", got, tt.want)
			}
			if configured.chargeOnceTarget != tt.wantLeft {
				t.Fatalf("chargeOnceTarget = %d, want %d", configured.chargeOnceTarget, tt.wantLeft)
			}
			if configured.upper != 70 || configured.lower != 40 {
				t.Fatalf("configured band changed to %d/%d, want 70/40", configured.upper, configured.lower)
			}
		})
	}
}

// legacyChargeMock builds an SMC mock for the direct charge-control backend.
func legacyChargeMock(t *testing.T, charge int, chargingEnabled bool) *smc.AppleSMC {
	t.Helper()
	value := func(key string, dataType gosmc.DataType, data ...byte) gosmc.Value {
		v, err := gosmc.NewValue(key, dataType, data)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}

	chargingByte := byte(0x02)
	if chargingEnabled {
		chargingByte = 0x00
	}
	mock := smc.NewMockValues(
		value(smc.ChargingKey1, gosmc.TypeUInt8, chargingByte),
		value(smc.ChargingKey2, gosmc.TypeUInt8, chargingByte),
		value(smc.BatteryChargeKey, gosmc.TypeUInt8, byte(charge)),
		value(smc.ACPowerKey, gosmc.TypeUInt8, 1),
	)
	if err := mock.Open(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mock.Close() })
	return mock
}

func useLegacyLoopState(t *testing.T, mock *smc.AppleSMC, configured *mockConf) {
	t.Helper()
	previousSMC, previousConf, previousCapabilities := smcConn, conf, capabilities
	previousState := calibrationState
	t.Cleanup(func() {
		smcConn, conf, capabilities = previousSMC, previousConf, previousCapabilities
		calibrationState = previousState
	})
	smcConn = mock
	conf = configured
	calibrationState = &calibration.State{Phase: calibration.PhaseIdle}
	capabilities = compatibility.Capabilities{
		ChargingControl:   true,
		ChargeControlMode: compatibility.ChargeControlLegacy,
	}
}

func TestLegacyMaintainLoopRespectsChargeOnceTarget(t *testing.T) {
	tests := []struct {
		name         string
		charge       int
		target       int
		charging     bool
		wantCharging bool
	}{
		{
			// Without a one-time charge, 58% inside a 40-70% band keeps its
			// state: this is the hysteresis gap.
			name: "hysteresis gap without a one-time charge", charge: 58, charging: false, wantCharging: false,
		},
		{
			name: "one-time charge starts charging inside the gap", charge: 58, target: 70, charging: false, wantCharging: true,
		},
		{
			name: "one-time charge stops at its target", charge: 70, target: 70, charging: true, wantCharging: false,
		},
		{
			name: "one-time charge to full keeps charging above the limit", charge: 80, target: 100, charging: true, wantCharging: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := legacyChargeMock(t, tt.charge, tt.charging)
			useLegacyLoopState(t, mock, &mockConf{upper: 70, lower: 40, chargeOnceTarget: tt.target})

			if !maintainLoopForced() {
				t.Fatal("legacy maintain loop failed")
			}
			charging, err := mock.IsChargingEnabled()
			if err != nil {
				t.Fatal(err)
			}
			if charging != tt.wantCharging {
				t.Fatalf("charging = %v, want %v", charging, tt.wantCharging)
			}
		})
	}
}

// firmwareChargeMock builds an SMC mock for the firmware charge-control backend.
func firmwareChargeMock(t *testing.T) *smc.AppleSMC {
	t.Helper()
	value := func(key string, dataType gosmc.DataType, data ...byte) gosmc.Value {
		v, err := gosmc.NewValue(key, dataType, data)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}

	mock := smc.NewMockValues(
		value(smc.FirmwareChargeLimitActivationKey, gosmc.TypeUInt8, 0),
		value(smc.FirmwareChargeLimitUpperKey, gosmc.TypeUInt32, 0, 0, 0, 0),
		value(smc.FirmwareChargeLimitLowerKey, gosmc.TypeUInt32, 0, 0, 0, 0),
	)
	if err := mock.Open(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mock.Close() })
	return mock
}

func useFirmwareLoopState(t *testing.T, mock *smc.AppleSMC, configured *mockConf) {
	t.Helper()
	previousSMC, previousConf, previousCapabilities := smcConn, conf, capabilities
	previousState := calibrationState
	t.Cleanup(func() {
		smcConn, conf, capabilities = previousSMC, previousConf, previousCapabilities
		calibrationState = previousState
	})
	smcConn = mock
	conf = configured
	calibrationState = &calibration.State{Phase: calibration.PhaseIdle}
	capabilities = compatibility.Capabilities{
		ChargingControl:   true,
		ChargeControlMode: compatibility.ChargeControlFirmware,
	}
}

func TestFirmwareMaintainLoopAppliesChargeOnceBand(t *testing.T) {
	mock := firmwareChargeMock(t)
	configured := &mockConf{upper: 70, lower: 40, chargeOnceTarget: 70}
	useFirmwareLoopState(t, mock, configured)

	// The firmware API rejects lower >= upper, so a one-time charge to the
	// configured limit uses the narrowest legal band below it.
	if !maintainLoopForced() {
		t.Fatal("firmware maintain loop failed")
	}
	state, err := mock.GetFirmwareChargeLimit()
	if err != nil {
		t.Fatal(err)
	}
	if !state.Active || state.Lower != 69 || state.Upper != 70 {
		t.Fatalf("firmware state = %+v, want active 69/70", state)
	}

	// Charging to full hands the range back to the firmware default.
	configured.chargeOnceTarget = 100
	if !maintainLoopForced() {
		t.Fatal("firmware maintain loop failed")
	}
	state, err = mock.GetFirmwareChargeLimit()
	if err != nil {
		t.Fatal(err)
	}
	if state.Active {
		t.Fatalf("firmware limit is still active during a one-time charge to 100%%: %+v", state)
	}

	// Once the one-time charge ends, the configured band applies again.
	configured.chargeOnceTarget = 0
	if !maintainLoopForced() {
		t.Fatal("firmware maintain loop failed")
	}
	state, err = mock.GetFirmwareChargeLimit()
	if err != nil {
		t.Fatal(err)
	}
	if !state.Active || state.Lower != 40 || state.Upper != 70 {
		t.Fatalf("firmware state = %+v, want active 40/70", state)
	}
}

func TestChargeOnceReachedTarget(t *testing.T) {
	tests := []struct {
		name   string
		mode   compatibility.ChargeControlMode
		target int
		charge int
		want   bool
	}{
		{name: "legacy stops at the target", mode: compatibility.ChargeControlLegacy, target: 80, charge: 80, want: true},
		{name: "legacy does not accept one percent short", mode: compatibility.ChargeControlLegacy, target: 80, charge: 79},
		// The firmware band for a one-time charge to 80% is 79/80, and the
		// firmware never resumes charging at 79%.
		{name: "firmware accepts the top of its band", mode: compatibility.ChargeControlFirmware, target: 80, charge: 79, want: true},
		{name: "firmware keeps charging below the band", mode: compatibility.ChargeControlFirmware, target: 80, charge: 78},
		// A one-time charge to 100% deactivates the limit instead of narrowing
		// the band, so it has no percent to give away.
		{name: "firmware charge to full needs 100%", mode: compatibility.ChargeControlFirmware, target: 100, charge: 99},
		{name: "firmware charge to full reached", mode: compatibility.ChargeControlFirmware, target: 100, charge: 100, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			previous := capabilities
			t.Cleanup(func() { capabilities = previous })
			capabilities = compatibility.Capabilities{ChargingControl: true, ChargeControlMode: tt.mode}

			if got := chargeOnceReachedTarget(tt.target, tt.charge); got != tt.want {
				t.Fatalf("chargeOnceReachedTarget(%d, %d) = %v, want %v", tt.target, tt.charge, got, tt.want)
			}
		})
	}
}

func TestFirmwareChargeOnceCompletesAtTheTopOfItsBand(t *testing.T) {
	// A one-time charge to the 80% limit started at 79% cannot move: the
	// firmware only resumes charging below the lower bound of the 79/80 band it
	// gets. It must still end and hand the configured band back.
	mock := firmwareChargeMock(t)
	configured := &mockConf{upper: 80, lower: 78, chargeOnceTarget: 80}
	useFirmwareLoopState(t, mock, configured)
	stubBatteryCharge(t, 79)

	if !maintainLoopForced() {
		t.Fatal("firmware maintain loop failed")
	}
	state, err := mock.GetFirmwareChargeLimit()
	if err != nil {
		t.Fatal(err)
	}
	if !state.Active || state.Lower != 79 || state.Upper != 80 {
		t.Fatalf("firmware state = %+v, want active 79/80", state)
	}

	if !completeChargeOnce(configured) {
		t.Fatal("completeChargeOnce() = false, want true at the top of the one-time band")
	}
	if configured.chargeOnceTarget != 0 {
		t.Fatalf("chargeOnceTarget = %d, want 0", configured.chargeOnceTarget)
	}

	// The configured band applies again, so the persistent limit is restored.
	if !maintainLoopForced() {
		t.Fatal("firmware maintain loop failed")
	}
	state, err = mock.GetFirmwareChargeLimit()
	if err != nil {
		t.Fatal(err)
	}
	if !state.Active || state.Lower != 78 || state.Upper != 80 {
		t.Fatalf("firmware state = %+v, want active 78/80", state)
	}
}

func TestCancelChargeOnceKeepsTheTargetWhenSaveFails(t *testing.T) {
	// The config file still holds the target after a failed save, so dropping
	// it from memory would make a retry report that nothing is running while a
	// restart resumes the cancelled one-time charge.
	configured := &mockConf{upper: 70, lower: 40, chargeOnceTarget: 100, saveErr: errors.New("disk full")}
	useChargeOnceDaemonState(t, configured, calibration.PhaseIdle)

	response := postChargeOnce("/charge-once/cancel")
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusInternalServerError, response.Body.String())
	}
	if configured.chargeOnceTarget != 100 {
		t.Fatalf("chargeOnceTarget = %d, want 100 after a failed save", configured.chargeOnceTarget)
	}

	// The retry after a working disk cancels as usual.
	configured.saveErr = nil
	response = postChargeOnce("/charge-once/cancel")
	if response.Code != http.StatusOK {
		t.Fatalf("retry status = %d, want %d; body: %s", response.Code, http.StatusOK, response.Body.String())
	}
	if configured.chargeOnceTarget != 0 {
		t.Fatalf("chargeOnceTarget = %d, want 0", configured.chargeOnceTarget)
	}
}

func TestStartChargeOnceKeepsNoTargetWhenSaveFails(t *testing.T) {
	configured := &mockConf{upper: 70, lower: 40, saveErr: errors.New("disk full")}
	useChargeOnceDaemonState(t, configured, calibration.PhaseIdle)
	stubBatteryCharge(t, 58)

	response := postChargeOnce("/charge-once/limit")
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusInternalServerError, response.Body.String())
	}
	if configured.chargeOnceTarget != 0 {
		t.Fatalf("chargeOnceTarget = %d, want 0 after a failed save", configured.chargeOnceTarget)
	}
}
