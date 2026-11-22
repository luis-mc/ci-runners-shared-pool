package machines

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

// IsDefaultSettings says whether the settings an owner chooses are all still at
// their defaults: backends, pool size, policy and compact policy.
func IsDefaultSettings(s Settings) bool {
	d := DefaultSettings()
	return slices.Equal(s.AllowedBackends, d.AllowedBackends) && s.PoolMemoryGiB == 0 && s.PoolCPUs == 0 &&
		s.EffectiveMode() == d.Mode && s.EffectiveCPUCap() == d.CPUCapPct && s.PauseOnBattery == d.PauseOnBattery && s.DiskFloorGiB == 0 &&
		s.CompactMode == "" && s.CompactThresholdGiB == 0 && s.CompactIdleMinutes == 0
}

// ResetSigns gives, for each machine, the signs that its settings went back to
// the defaults (doctor machine.reset, R5.70): it joined again, or it has default
// settings while an earlier machine of the same host, OS and architecture had
// chosen ones. The key is the machine id.
func (s *Service) ResetSigns(ctx context.Context) (map[string][]string, error) {
	ms, err := s.o.Repo.ListMachines(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string][]string{}
	for _, m := range ms {
		if !m.RejoinedAt.IsZero() {
			out[m.ID] = append(out[m.ID], fmt.Sprintf("it joined again at %s and kept its settings and runners", m.RejoinedAt.UTC().Format("2006-01-02 15:04 MST")))
		}
		set, err := s.o.Repo.GetSettings(ctx, m.ID)
		if err != nil || !IsDefaultSettings(set) {
			continue
		}
		for _, o := range ms {
			if o.ID == m.ID || !o.JoinedAt.Before(m.JoinedAt) || o.OS != m.OS || o.Arch != m.Arch ||
				!strings.EqualFold(strings.TrimSpace(o.Facts.HostName), strings.TrimSpace(m.Facts.HostName)) || strings.TrimSpace(m.Facts.HostName) == "" {
				continue
			}
			if os, err := s.o.Repo.GetSettings(ctx, o.ID); err == nil && !IsDefaultSettings(os) {
				out[m.ID] = append(out[m.ID], fmt.Sprintf("its settings are the defaults, but the earlier machine %s of the same computer had chosen ones", o.Name))
				break
			}
		}
	}
	return out, nil
}
