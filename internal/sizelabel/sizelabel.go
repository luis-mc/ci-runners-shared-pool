// Package sizelabel reads free-form size labels in a workflow's runs-on, such
// as cpu=4 and ram=8, into a CPU and memory size. The size becomes the limits
// of the job's container and decides which machine can take the job
// (scheduling R5.67a, CRSP264). It reads text only, so it behaves the same on
// Windows, Linux and macOS.
package sizelabel

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// The label keys the pool understands.
const (
	KeyCPU = "cpu"
	KeyRAM = "ram"
)

// Limits of one label value. A job may ask for no more than the largest
// machine could ever hold; a machine's own limit is checked in Fit.
const (
	MaxCPUs      = 256
	MaxMemoryGiB = 4096
	MinMemoryGiB = 0.5
)

// Spec is the size a job asks for. Zero means "not asked".
type Spec struct {
	CPUs      float64
	MemoryGiB float64
}

// Empty is true when the job asked for no size.
func (s Spec) Empty() bool { return s.CPUs == 0 && s.MemoryGiB == 0 }

// Error names the label that was refused and why.
type Error struct {
	Label  string
	Reason string
}

func (e *Error) Error() string { return fmt.Sprintf("size label %q refused: %s", e.Label, e.Reason) }

// IsSizeLabel is true for a label shaped like key=value with a size key. It
// does not check the value; Parse does.
func IsSizeLabel(l string) bool {
	k, _, ok := strings.Cut(strings.TrimSpace(l), "=")
	return ok && isKey(strings.ToLower(strings.TrimSpace(k)))
}

func isKey(k string) bool { return k == KeyCPU || k == KeyRAM }

// Parse reads the size labels among labels and ignores every other label.
// A key given twice, an unknown value or a value out of range is refused with
// the label and the reason. Labels without "=" and labels with another key are
// not size labels and are left alone.
func Parse(labels []string) (Spec, error) {
	var s Spec
	seen := map[string]bool{}
	for _, raw := range labels {
		l := strings.TrimSpace(raw)
		k, v, ok := strings.Cut(l, "=")
		if !ok {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(k))
		if !isKey(key) {
			continue
		}
		if seen[key] {
			return Spec{}, &Error{Label: l, Reason: fmt.Sprintf("%s is given twice; keep one", key)}
		}
		seen[key] = true
		val := strings.TrimSpace(v)
		switch key {
		case KeyCPU:
			n, err := number(val, "")
			if err != nil {
				return Spec{}, &Error{Label: l, Reason: "cpu needs a number of CPUs, such as cpu=4 (" + err.Error() + ")"}
			}
			if n < 1 || n > MaxCPUs || n != math.Trunc(n) {
				return Spec{}, &Error{Label: l, Reason: fmt.Sprintf("cpu must be a whole number from 1 to %d", MaxCPUs)}
			}
			s.CPUs = n
		case KeyRAM:
			n, err := number(val, "gib")
			if err != nil {
				return Spec{}, &Error{Label: l, Reason: "ram needs gibibytes, such as ram=8 or ram=8gb (" + err.Error() + ")"}
			}
			if n < MinMemoryGiB || n > MaxMemoryGiB {
				return Spec{}, &Error{Label: l, Reason: fmt.Sprintf("ram must be from %g to %d GiB", MinMemoryGiB, MaxMemoryGiB)}
			}
			s.MemoryGiB = n
		}
	}
	return s, nil
}

// number reads "8", "8.5" or "8gb"/"8g"/"8gib" (when unit is "gib").
func number(v, unit string) (float64, error) {
	v = strings.ToLower(v)
	if unit == "gib" {
		for _, suf := range []string{"gib", "gb", "g"} {
			if strings.HasSuffix(v, suf) {
				v = strings.TrimSuffix(v, suf)
				break
			}
		}
	}
	if v == "" {
		return 0, fmt.Errorf("no value")
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
		return 0, fmt.Errorf("%q is not a number", v)
	}
	return n, nil
}

// Limits are the caps for a job's container (docker --memory and --cpus).
type Limits struct {
	MemoryMB int
	CPUs     float64
}

// ContainerLimits turns a size into container caps. A part not asked is zero
// (no cap from this label).
func (s Spec) ContainerLimits() Limits {
	return Limits{MemoryMB: int(math.Round(s.MemoryGiB * 1024)), CPUs: s.CPUs}
}

// Machine is the room one machine offers (its pool size, machines R5.58).
type Machine struct {
	Name      string
	OS        string // linux, windows or macos
	CPUs      float64
	MemoryGiB float64
}

// Fit says why a machine cannot hold the size; nil when it can. A machine with
// no CPU or memory number known (zero) is not checked on that part.
func (s Spec) Fit(m Machine) error {
	if m.CPUs > 0 && s.CPUs > m.CPUs {
		return fmt.Errorf("machine %s has %g CPUs, the job asks for cpu=%g", m.Name, m.CPUs, s.CPUs)
	}
	if m.MemoryGiB > 0 && s.MemoryGiB > m.MemoryGiB {
		return fmt.Errorf("machine %s has %g GiB memory, the job asks for ram=%g", m.Name, m.MemoryGiB, s.MemoryGiB)
	}
	return nil
}

// Clamp lowers a container limit to a machine's own room: a job never gets a
// cap above what the machine offers. Parts not asked keep the machine's own cap.
func (s Spec) Clamp(machine Limits) Limits {
	out := machine
	if l := s.ContainerLimits(); l.MemoryMB > 0 && (out.MemoryMB == 0 || l.MemoryMB < out.MemoryMB) {
		out.MemoryMB = l.MemoryMB
	}
	if s.CPUs > 0 && (out.CPUs == 0 || s.CPUs < out.CPUs) {
		out.CPUs = s.CPUs
	}
	return out
}

// Place picks the machine for a size: among the machines of the OS that can
// hold it, the one with the least room to spare, so large machines stay free
// for large jobs. Ties go to the lower name so the answer is stable. When no
// machine fits, the error says why for each one. An empty size takes the
// smallest machine of the OS too. os "" matches every OS.
func Place(s Spec, os string, machines []Machine) (Machine, error) {
	var fit []Machine
	var why []string
	for _, m := range machines {
		if os != "" && !strings.EqualFold(m.OS, os) {
			continue
		}
		if err := s.Fit(m); err != nil {
			why = append(why, err.Error())
			continue
		}
		fit = append(fit, m)
	}
	if len(fit) == 0 {
		if len(why) == 0 {
			return Machine{}, fmt.Errorf("no machine runs %s jobs", os)
		}
		sort.Strings(why)
		return Machine{}, fmt.Errorf("no machine can hold the size: %s", strings.Join(why, "; "))
	}
	sort.SliceStable(fit, func(i, j int) bool {
		a, b := fit[i], fit[j]
		if a.CPUs != b.CPUs {
			return a.CPUs < b.CPUs
		}
		if a.MemoryGiB != b.MemoryGiB {
			return a.MemoryGiB < b.MemoryGiB
		}
		return a.Name < b.Name
	})
	return fit[0], nil
}

// Understood is one label the pool understands, for the labels screen.
type Understood struct {
	Key     string
	Meaning string
	Example string
	Min     string
	Max     string
}

// Understands lists the labels the pool reads, in the order the screen shows.
func Understands() []Understood {
	return []Understood{
		{Key: KeyCPU, Meaning: "CPUs the job container may use", Example: "cpu=4", Min: "1", Max: strconv.Itoa(MaxCPUs)},
		{Key: KeyRAM, Meaning: "Memory in GiB the job container may use (ram=8 or ram=8gb)", Example: "ram=8", Min: strconv.FormatFloat(MinMemoryGiB, 'f', -1, 64), Max: strconv.Itoa(MaxMemoryGiB)},
	}
}

// Example is the copy-paste runs-on line for the screen.
const Example = "runs-on: [self-hosted, linux, cpu=4, ram=8]"
