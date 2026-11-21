package runners

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ClassSizes is the editable table of the four size classes: CPUs and GiB of
// memory per class (scheduling R5.89). Runners own no CPUs; a class is the
// ceiling of the jobs a runner takes (R5.90) and the hard memory cap of each
// job (R5.95).
type ClassSizes struct {
	Small, Medium, Large, XL SizeSpec
}

// Preset names (scheduling R5.89).
const (
	PresetStandard    = "standard"
	PresetCompact     = "compact"
	PresetMemoryHeavy = "memory-heavy"
	PresetCustom      = "custom"
)

// PresetNames lists the presets, the recommended one first.
func PresetNames() []string {
	return []string{PresetStandard, PresetCompact, PresetMemoryHeavy, PresetCustom}
}

func sizes(sc, sm, mc, mm, lc, lm, xc, xm int) ClassSizes {
	return ClassSizes{
		Small:  SizeSpec{SizeSmall, sc, sm},
		Medium: SizeSpec{SizeMedium, mc, mm},
		Large:  SizeSpec{SizeLarge, lc, lm},
		XL:     SizeSpec{SizeXL, xc, xm},
	}
}

// Preset returns the table of a named preset. Standard is the built-in table
// (`sizeTable`); Custom starts from it. ok is false for an unknown name.
func Preset(name string) (ClassSizes, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", PresetStandard, PresetCustom:
		return TableSizes(), true
	case PresetCompact: // pools up to 8 CPUs
		return sizes(1, 2, 2, 4, 4, 8, 8, 16), true
	case PresetMemoryHeavy: // browsers, Java
		return sizes(2, 6, 4, 12, 8, 24, 16, 48), true
	}
	return ClassSizes{}, false
}

// TableSizes is the built-in table as ClassSizes.
func TableSizes() ClassSizes {
	return ClassSizes{Small: sizeTable[0], Medium: sizeTable[1], Large: sizeTable[2], XL: sizeTable[3]}
}

// List returns the four specs, smallest first.
func (c ClassSizes) List() []SizeSpec { return []SizeSpec{c.Small, c.Medium, c.Large, c.XL} }

// Spec gives one class; ok is false for an unknown class.
func (c ClassSizes) Spec(cl SizeClass) (SizeSpec, bool) {
	for _, s := range c.List() {
		if s.Class == cl {
			return s, true
		}
	}
	return SizeSpec{}, false
}

// Smallest gives the smallest class that holds cpus and memGiB. ok is false
// when even xl is too small.
func (c ClassSizes) Smallest(cpus, memGiB float64) (SizeSpec, bool) {
	for _, s := range c.List() {
		if float64(s.CPUs) >= cpus && float64(s.MemoryGiB) >= memGiB {
			return s, true
		}
	}
	return SizeSpec{}, false
}

// Preset names the preset the table equals, or custom.
func (c ClassSizes) Preset() string {
	for _, n := range []string{PresetStandard, PresetCompact, PresetMemoryHeavy} {
		if p, _ := Preset(n); p == c {
			return n
		}
	}
	return PresetCustom
}

// The words of a refused save (scheduling R5.91). The owner chose them.
const (
	MsgWholeNumbers = "Enter whole numbers above 0."
)

// ErrClassTable is the error of a refused table; its text is the owner's words.
type ErrClassTable struct{ Msg string }

func (e *ErrClassTable) Error() string { return e.Msg }

func label(c SizeClass) string {
	s := string(c)
	if c == SizeXL {
		return "XL"
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// Validate applies the hard rules: whole numbers above 0, then strict growth
// from small to xl in CPUs and in memory (scheduling R5.91).
func (c ClassSizes) Validate() error {
	l := c.List()
	for _, s := range l {
		if s.CPUs < 1 || s.MemoryGiB < 1 {
			return &ErrClassTable{MsgWholeNumbers}
		}
	}
	for i := 1; i < len(l); i++ {
		prev, cur := l[i-1], l[i]
		if cur.CPUs <= prev.CPUs {
			return &ErrClassTable{fmt.Sprintf("%s has %d CPUs; it must have more than %s's %d. Sizes grow from small to xl.",
				label(cur.Class), cur.CPUs, strings.ToLower(label(prev.Class)), prev.CPUs)}
		}
		if cur.MemoryGiB <= prev.MemoryGiB {
			return &ErrClassTable{fmt.Sprintf("%s has %d GiB; it must have more than %s's %d GiB. Sizes grow from small to xl.",
				label(cur.Class), cur.MemoryGiB, strings.ToLower(label(prev.Class)), prev.MemoryGiB)}
		}
	}
	return nil
}

// Hints are notes that never block a save (scheduling R5.91): a step between
// neighbours under 1.5x, memory outside 1.5 to 8 GiB per CPU, a class below the
// floor of 1 CPU and 2 GiB.
func (c ClassSizes) Hints() []string {
	var out []string
	l := c.List()
	for i, s := range l {
		if s.CPUs < 1 || s.MemoryGiB < 2 {
			out = append(out, fmt.Sprintf("%s is below the floor of 1 CPU and 2 GiB.", label(s.Class)))
		}
		if s.CPUs > 0 {
			per := float64(s.MemoryGiB) / float64(s.CPUs)
			if per < 1.5 || per > 8 {
				out = append(out, fmt.Sprintf("%s has %.1f GiB per CPU; 1.5 to 8 is usual.", label(s.Class), per))
			}
		}
		if i > 0 {
			p := l[i-1]
			if p.CPUs > 0 && float64(s.CPUs) < 1.5*float64(p.CPUs) {
				out = append(out, fmt.Sprintf("%s is less than 1.5 times %s in CPUs.", label(s.Class), strings.ToLower(label(p.Class))))
			}
			if p.MemoryGiB > 0 && float64(s.MemoryGiB) < 1.5*float64(p.MemoryGiB) {
				out = append(out, fmt.Sprintf("%s is less than 1.5 times %s in memory.", label(s.Class), strings.ToLower(label(p.Class))))
			}
		}
	}
	return out
}

// PoolRoom is the pool of one machine: what a runner's class must fit in.
type PoolRoom struct {
	Machine   string
	CPUs      int
	MemoryGiB int
}

// CheckRunnerFits refuses a class whose ceiling the machine's pool cannot hold
// (scheduling R5.91). A pool of 0 is not known and is not checked.
func (c ClassSizes) CheckRunnerFits(cl SizeClass, p PoolRoom) error {
	s, ok := c.Spec(cl)
	if !ok {
		return &ErrClassTable{fmt.Sprintf("size class %q is not one of small, medium, large, xl", cl)}
	}
	if p.CPUs <= 0 || p.MemoryGiB <= 0 {
		return nil
	}
	if s.CPUs <= p.CPUs && s.MemoryGiB <= p.MemoryGiB {
		return nil
	}
	pick := ""
	for _, x := range c.List() {
		if x.CPUs <= p.CPUs && x.MemoryGiB <= p.MemoryGiB {
			pick = strings.ToLower(label(x.Class))
		}
	}
	msg := fmt.Sprintf("%s needs %d CPUs and %d GiB. %s's pool is %d CPUs and %d GiB.",
		label(cl), s.CPUs, s.MemoryGiB, p.Machine, p.CPUs, p.MemoryGiB)
	if pick != "" {
		msg += fmt.Sprintf(" Pick %s for this runner, or raise the pool on the machine page.", pick)
	} else {
		msg += " Raise the pool on the machine page."
	}
	return &ErrClassTable{msg}
}

// UnheldSizes lists the classes no pool in pools can hold. It is a note, not an
// error (scheduling R5.91).
func (c ClassSizes) UnheldSizes(pools []PoolRoom) []SizeClass {
	var out []SizeClass
	for _, s := range c.List() {
		held := false
		for _, p := range pools {
			if s.CPUs <= p.CPUs && s.MemoryGiB <= p.MemoryGiB {
				held = true
				break
			}
		}
		if !held {
			out = append(out, s.Class)
		}
	}
	return out
}

// ErrConfirmLowering is returned when a save lowers a class that runners use
// and the confirm flag is not set (scheduling R5.92, like machines R5.58).
var ErrConfirmLowering = errors.New("a size that runners use is lowered; confirm to save")

// LoweredInUse names the classes that get fewer CPUs or less memory in next
// than in prev and that a runner of inUse has.
func LoweredInUse(prev, next ClassSizes, inUse map[SizeClass]int) []SizeClass {
	var out []SizeClass
	for _, p := range prev.List() {
		n, ok := next.Spec(p.Class)
		if !ok || inUse[p.Class] == 0 {
			continue
		}
		if n.CPUs < p.CPUs || n.MemoryGiB < p.MemoryGiB {
			out = append(out, p.Class)
		}
	}
	return out
}

// CheckSave runs the whole save check in the order of the rules: the table,
// then the fit of every runner, then the lowering confirm.
func CheckSave(prev, next ClassSizes, runners map[string]SizeClass, pools map[string]PoolRoom, inUse map[SizeClass]int, confirm bool) error {
	if err := next.Validate(); err != nil {
		return err
	}
	for name, cl := range runners {
		if p, ok := pools[name]; ok {
			if err := next.CheckRunnerFits(cl, p); err != nil {
				return err
			}
		}
	}
	if !confirm && len(LoweredInUse(prev, next, inUse)) > 0 {
		return ErrConfirmLowering
	}
	return nil
}

type classJSON struct {
	CPUs      int `json:"cpus"`
	MemoryGiB int `json:"memory_gib"`
}

// MarshalJSON writes {"small":{"cpus":2,"memory_gib":4},...}.
func (c ClassSizes) MarshalJSON() ([]byte, error) {
	m := map[string]classJSON{}
	for _, s := range c.List() {
		m[string(s.Class)] = classJSON{s.CPUs, s.MemoryGiB}
	}
	return json.Marshal(m)
}

// UnmarshalJSON reads what MarshalJSON writes; a class that is missing keeps
// the built-in size.
func (c *ClassSizes) UnmarshalJSON(b []byte) error {
	m := map[string]classJSON{}
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	out := TableSizes()
	for _, p := range []*SizeSpec{&out.Small, &out.Medium, &out.Large, &out.XL} {
		if v, ok := m[string(p.Class)]; ok {
			p.CPUs, p.MemoryGiB = v.CPUs, v.MemoryGiB
		}
	}
	*c = out
	return nil
}
