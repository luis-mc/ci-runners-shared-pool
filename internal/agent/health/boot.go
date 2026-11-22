package health

import (
	"encoding/json"
	"time"
)

// bootState is kept in the data dir, so a restart of the agent or of the
// machine can be told from an expected one.
type bootState struct {
	BootMs       int64 `json:"boot_ms"`
	ExpectedMs   int64 `json:"expected_ms"`
	UnexpectedMs int64 `json:"unexpected_ms"`
}

// bootTolerance is how far two reads of the boot time may differ before it is a new boot.
const bootTolerance = 2 * time.Minute

// unexpectedFor is how long an unexpected restart stays flagged.
const unexpectedFor = 24 * time.Hour

// MarkExpectedRestart records that crsp is about to restart or shut down the
// machine (a host command or an update), so the next boot is not flagged.
func MarkExpectedRestart(env Env, stateFile string) error {
	st := readBootState(env, stateFile)
	st.ExpectedMs = env.Now().UnixMilli()
	return writeBootState(env, stateFile, st)
}

func readBootState(env Env, file string) bootState {
	var st bootState
	if txt, err := env.ReadFile(file); err == nil {
		_ = json.Unmarshal([]byte(txt), &st)
	}
	return st
}

func writeBootState(env Env, file string, st bootState) error {
	b, _ := json.Marshal(st)
	return env.WriteFile(file, b)
}

// trackBoot compares this boot with the last one seen and says whether the
// machine restarted without crsp asking for it in the last 24 hours.
func trackBoot(env Env, file string, bootMs int64) bool {
	if file == "" || bootMs == 0 {
		return false
	}
	st := readBootState(env, file)
	now := env.Now().UnixMilli()
	changed := false
	switch {
	case st.BootMs == 0:
		st.BootMs, changed = bootMs, true
	case abs64(bootMs-st.BootMs) > bootTolerance.Milliseconds():
		if st.ExpectedMs == 0 || st.ExpectedMs > bootMs {
			st.UnexpectedMs = now
		}
		st.BootMs, st.ExpectedMs, changed = bootMs, 0, true
	}
	if changed {
		_ = writeBootState(env, file, st)
	}
	return st.UnexpectedMs > 0 && now-st.UnexpectedMs < unexpectedFor.Milliseconds()
}

func abs64(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}
