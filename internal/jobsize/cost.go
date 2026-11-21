package jobsize

import (
	"encoding/json"
	"sort"
)

// Cost is what one worker of a framework, or one service, takes. Every value
// of DefaultCostTable is an assumption until measured sizes replace it (the
// measuring is OPS66).
type Cost struct {
	Class Class `json:"class"`
	// CPU is the CPUs one worker holds; GiBMin to GiB is the memory range of
	// one worker. The peak uses GiB, the top of the range.
	CPU    float64 `json:"cpu"`
	GiBMin float64 `json:"gib_min,omitempty"`
	GiB    float64 `json:"gib"`
	// SecPerTest is the time of one test run, for the time estimate only.
	SecPerTest float64 `json:"sec_per_test,omitempty"`
	// Assumption marks a value nobody measured.
	Assumption bool   `json:"assumption"`
	Note       string `json:"note,omitempty"`
}

// CostTable is the one editable table: a cost per framework and per service.
// The defaults live here; the controller keeps edits as a JSON patch in its
// settings (key SettingKey), and a measured value replaces a row later.
type CostTable struct {
	Frameworks map[string]Cost `json:"frameworks"`
	Services   map[string]Cost `json:"services"`
}

// SettingKey is the settings key of the edited rows.
const SettingKey = "job_cost_table"

// DefaultWorkers is the worker count of a tool that does not say (an
// assumption: a hosted runner has 2 to 4 CPUs).
const DefaultWorkers = 2

// DefaultCostTable is the starting table (owner decision, 21 Nov 2026). Every
// row is an assumption.
func DefaultCostTable() CostTable {
	fw := func(class Class, cpu, lo, hi, sec float64, note string) Cost {
		return Cost{Class: class, CPU: cpu, GiBMin: lo, GiB: hi, SecPerTest: sec, Assumption: true, Note: note}
	}
	svc := func(class Class, cpu, gib float64, note string) Cost {
		return Cost{Class: class, CPU: cpu, GiBMin: gib, GiB: gib, Assumption: true, Note: note}
	}
	return CostTable{
		Frameworks: map[string]Cost{
			"playwright": fw(Normal, 1, 0.5, 1, 10, "a browser worker"),
			"vitest":     fw(Light, 1, 0.3, 0.5, 0, "Vitest and Jest"),
			"go":         fw(Light, 1, 0.25, 0.5, 0, "a package test; the memory is a guess"),
			"rust":       fw(Normal, 1, 1, 2, 0, "the compile of one job"),
			"pytest":     fw(Light, 1, 0.2, 0.5, 0, ""),
			"jvm":        fw(Normal, 1, 1, 2, 0, "JUnit through Gradle or Maven; a guess"),
			"dotnet":     fw(Normal, 1, 0.5, 1, 0, "xUnit, NUnit, MSTest; a guess"),
		},
		Services: map[string]Cost{
			"sqlserver":      svc(Heavy, 1, 2.5, "over 2 GiB"),
			"kafka":          svc(Heavy, 1, 2.5, "over 2 GiB"),
			"elasticsearch":  svc(Heavy, 1, 2.5, "over 2 GiB; also OpenSearch"),
			"postgres":       svc(Normal, 0.5, 0.5, ""),
			"mysql":          svc(Normal, 0.5, 0.5, "also MariaDB"),
			"mongo":          svc(Normal, 0.5, 0.5, ""),
			"redis":          svc(Light, 0.25, 0.25, ""),
			"rabbitmq":       svc(Normal, 0.5, 0.5, ""),
			"testcontainers": svc(Normal, 1, 1, "containers a test starts itself"),
			"emulator":       svc(Heavy, 2, 4, "emulators and simulators"),
			"kvm":            svc(Heavy, 2, 4, "/dev/kvm"),
			"gpu":            svc(Heavy, 2, 4, ""),
		},
	}
}

// Merge applies the edited rows over t. A row with a bad class or a negative
// number is dropped. It returns a new table.
func (t CostTable) Merge(patch CostTable) CostTable {
	out := CostTable{Frameworks: map[string]Cost{}, Services: map[string]Cost{}}
	for k, v := range t.Frameworks {
		out.Frameworks[k] = v
	}
	for k, v := range t.Services {
		out.Services[k] = v
	}
	ok := func(c Cost) bool { return ValidClass(c.Class) && c.CPU >= 0 && c.GiB >= 0 && c.GiBMin >= 0 }
	for k, v := range patch.Frameworks {
		if ok(v) {
			out.Frameworks[k] = v
		}
	}
	for k, v := range patch.Services {
		if ok(v) {
			out.Services[k] = v
		}
	}
	return out
}

// ParseCostPatch reads the saved JSON of edited rows. Bad JSON gives an empty
// patch and the error.
func ParseCostPatch(raw string) (CostTable, error) {
	var p CostTable
	if raw == "" {
		return p, nil
	}
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return CostTable{}, err
	}
	return p, nil
}

// Row is one row of the table in a stable order, for the console and the docs.
type Row struct {
	Kind string // framework or service
	Key  string
	Cost
}

// Rows lists the table, frameworks first, each part by key.
func (t CostTable) Rows() []Row {
	var out []Row
	for _, part := range []struct {
		kind string
		m    map[string]Cost
	}{{"framework", t.Frameworks}, {"service", t.Services}} {
		keys := make([]string, 0, len(part.m))
		for k := range part.m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			out = append(out, Row{Kind: part.kind, Key: k, Cost: part.m[k]})
		}
	}
	return out
}
