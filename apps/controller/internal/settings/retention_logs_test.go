package settings

import (
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/logs"
)

// TestLogLimitsMatchSettings: the Settings ranges and defaults are the R6.8 numbers of internal/logs.
func TestLogLimitsMatchSettings(t *testing.T) {
	d, h := Ranges["retention_logs_days"], Ranges["retention_logs_mib_per_machine"]
	if int64(d.Min) != logs.RetentionDays.Floor || int64(d.Max) != logs.RetentionDays.Ceiling {
		t.Errorf("days range %v vs %+v", d, logs.RetentionDays)
	}
	if int64(h.Min) != logs.RetentionMiBPerMachine.Floor || int64(h.Max) != logs.RetentionMiBPerMachine.Ceiling {
		t.Errorf("MiB range %v vs %+v", h, logs.RetentionMiBPerMachine)
	}
	if int64(Defaults.RetentionLogsDays) != logs.RetentionDays.Default || int64(Defaults.RetentionLogsMiBPerMachine) != logs.RetentionMiBPerMachine.Default {
		t.Errorf("defaults %+v", Defaults)
	}
}
