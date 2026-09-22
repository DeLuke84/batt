package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/spf13/cobra"

	"github.com/charlie0129/batt/pkg/compatibility"
	"github.com/charlie0129/batt/pkg/config"
	"github.com/charlie0129/batt/pkg/powerinfo"
)

func TestPrintStatusJSON_PreventSleepOnAdapterDisable(t *testing.T) {
	tests := []struct {
		name                 string
		adapterControl       bool
		preventSleep         bool
		wantPreventSleepJSON bool
		wantVal              bool
	}{
		{
			name:                 "supported and enabled",
			adapterControl:       true,
			preventSleep:         true,
			wantPreventSleepJSON: true,
			wantVal:              true,
		},
		{
			name:                 "supported and disabled",
			adapterControl:       true,
			preventSleep:         false,
			wantPreventSleepJSON: true,
			wantVal:              false,
		},
		{
			name:                 "unsupported adapter control omits field",
			adapterControl:       false,
			preventSleep:         false,
			wantPreventSleepJSON: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := &bytes.Buffer{}
			cmd := &cobra.Command{}
			cmd.SetOut(buf)

			limit := 80
			rawCfg := &config.RawFileConfig{
				Limit:                        &limit,
				PreventSleepOnAdapterDisable: &tt.preventSleep,
			}
			cfg := config.NewFileFromConfig(rawCfg, "")

			data := &statusData{
				capabilities: compatibility.Capabilities{
					ChargingControl: true,
					AdapterControl:  tt.adapterControl,
				},
				batteryInfo: &powerinfo.Battery{
					State: powerinfo.Charging,
				},
			}

			if err := printStatusJSON(cmd, data, cfg); err != nil {
				t.Fatalf("printStatusJSON failed: %v", err)
			}

			var parsed map[string]interface{}
			if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
				t.Fatalf("json unmarshal failed: %v", err)
			}

			configuration, ok := parsed["configuration"].(map[string]interface{})
			if !ok {
				t.Fatalf("configuration missing or not map: %v", parsed)
			}

			val, exists := configuration["preventSleepOnAdapterDisable"]
			if tt.wantPreventSleepJSON {
				if !exists {
					t.Fatalf("expected preventSleepOnAdapterDisable in configuration: %v", configuration)
				}
				if val != tt.wantVal {
					t.Fatalf("preventSleepOnAdapterDisable = %v, want %v", val, tt.wantVal)
				}
			} else {
				if exists {
					t.Fatalf("expected preventSleepOnAdapterDisable to be omitted when unsupported, got: %v", val)
				}
			}
		})
	}
}
