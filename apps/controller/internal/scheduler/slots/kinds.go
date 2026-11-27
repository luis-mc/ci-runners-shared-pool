package slots

import "strings"

// Kind is the slot class (R5.24 to R5.26).
type Kind string

// Slot kinds. CI is the normal job slot.
const (
	KindCI    Kind = "ci"
	KindLight Kind = "light"
	KindHeavy Kind = "heavy"
)

// JobNames are the names a light or heavy entry may match: the job key and the shard.
type JobNames struct {
	Repository string
	Job        string
	Shard      string
}

func (j JobNames) names() []string {
	var out []string
	for _, n := range []string{j.Job, j.Shard} {
		if n != "" {
			out = append(out, n)
		}
	}
	return out
}

// IsHeavy is true when the job key or shard matches a heavy entry and heavy
// slots are above 0. An entry owner/repo:pattern applies to that repository
// only (R5.25; machine_slots.py is_heavy_job).
func IsHeavy(j JobNames, s Settings) bool {
	if s.HeavySlots < 1 {
		return false
	}
	for _, pat := range s.HeavyJobs {
		if repo, rest, ok := strings.Cut(pat, ":"); ok {
			if repo != j.Repository {
				continue
			}
			pat = rest
		}
		for _, n := range j.names() {
			if matchGlob(pat, n) {
				return true
			}
		}
	}
	return false
}

// IsLight is true when the job key or shard matches a light entry and light
// slots are above 0. Heavy wins over light (R5.24; machine_slots.py is_light_job).
func IsLight(j JobNames, s Settings) bool {
	if s.LightSlots < 1 || IsHeavy(j, s) {
		return false
	}
	for _, pat := range s.LightJobs {
		for _, n := range j.names() {
			if matchGlob(pat, n) {
				return true
			}
		}
	}
	return false
}

// SlotLabel names a slot in logs: light-N, heavy-N or N (R5.24).
func SlotLabel(k Kind, n int) string {
	if k == KindCI || k == "" {
		return itoa(n)
	}
	return string(k) + "-" + itoa(n)
}
