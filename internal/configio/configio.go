// Package configio is config as code (operations R5.28 to R5.30, R6.10): the
// controller setup as one versioned file, a dry-run plan that shows what an
// apply would change, and an apply of exactly that plan.
//
// The engine knows nothing about pools, machines or repositories. Each area
// is a Section: it reads its items as plain fields and writes one change at
// a time. The engine owns the file format, the secret check, the diff, the
// plan ID tied to the current state, the removal yes and the audit row of
// every change. It is the same plan-then-apply shape as the repository plans.
package configio

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/luis-mc/ci-runners-shared-pool/internal/audit"
)

// FormatName and FormatVersion head every file. A file with a newer version
// is refused, so an old controller never half-reads a new file.
const (
	FormatName    = "crsp-config"
	FormatVersion = 1
)

// Fields are the settings of one item: plain JSON values only.
type Fields map[string]any

// Kind of a change.
type Kind string

// Change kinds (operations R5.29).
const (
	Added   Kind = "added"
	Changed Kind = "changed"
	Removed Kind = "removed"
)

// Section is one area of the setup. A singleton section has one item with an
// empty name; any other section lists items by unique name.
type Section interface {
	Name() string
	Singleton() bool
	// CanAdd and CanRemove say whether the file may create or delete items. A
	// machine joins by its join file, so it can be neither.
	CanAdd() bool
	CanRemove() bool
	// Read returns every item by name.
	Read(ctx context.Context) (map[string]Fields, error)
	// Write sets the item to want. old is nil for an added item. It reports
	// whether runners drain first (operations R5.14); the section's service
	// does that drain itself, the engine only records it.
	Write(ctx context.Context, name string, want, old Fields) error
	// Delete removes the item.
	Delete(ctx context.Context, name string) error
}

// FieldChange is one changed field.
type FieldChange struct {
	Field string `json:"field"`
	Old   any    `json:"old,omitempty"`
	New   any    `json:"new,omitempty"`
}

// Change is one line of a plan.
type Change struct {
	Section string        `json:"section"`
	Name    string        `json:"name"`
	Kind    Kind          `json:"kind"`
	Fields  []FieldChange `json:"fields,omitempty"`
}

// Ref names a change for the removal yes: "section/name".
func (c Change) Ref() string {
	if c.Name == "" {
		return c.Section
	}
	return c.Section + "/" + c.Name
}

// Plan is the answer of Plan. It changes nothing.
type Plan struct {
	ID string `json:"id"`
	// Problems are the reasons the file cannot be applied; a plan with problems
	// has no ID.
	Problems  []string  `json:"problems,omitempty"`
	Changes   []Change  `json:"changes"`
	CreatedAt time.Time `json:"created_at"`
	// Removals are the refs the apply needs a yes for.
	Removals []string `json:"removals,omitempty"`
}

// Empty reports whether applying would change nothing.
func (p Plan) Empty() bool { return len(p.Changes) == 0 }

// Result is the outcome of an apply.
type Result struct {
	PlanID  string   `json:"plan_id"`
	Applied []Change `json:"applied"`
}

// Errors.
var (
	// ErrStale: the setup changed since the plan (operations R5.30).
	ErrStale = errors.New("the setup changed since the plan; make a new plan")
	// ErrUnknownPlan: no plan with this ID (plans last until a restart).
	ErrUnknownPlan = errors.New("no such plan")
	// ErrBadFile: the file is not a valid config file.
	ErrBadFile = errors.New("not a valid config file")
)

// NeedsConfirmation is returned when a plan removes items and the apply was
// not given a yes that names each one.
type NeedsConfirmation struct{ Removals []string }

func (e *NeedsConfirmation) Error() string {
	return "removing " + strings.Join(e.Removals, ", ") + " needs a yes that names each one"
}

// Engine plans and applies config files over its sections.
type Engine struct {
	Sections []Section
	Audit    audit.Recorder
	Version  string
	Now      func() time.Time
	// Secrets lists the secrets by name for the references block of an
	// export; the values never reach the engine. Nil lists none.
	Secrets func(ctx context.Context) []SecretRef

	mu    sync.Mutex
	plans map[string]stored
}

type stored struct {
	plan  Plan
	state string
	file  []byte
}

// SecretRef is a secret named in the file: a reference, never a value.
type SecretRef struct {
	Name  string `yaml:"name" json:"name"`
	Ref   string `yaml:"ref" json:"ref"`
	State string `yaml:"state" json:"state"`
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e *Engine) section(name string) Section {
	for _, s := range e.Sections {
		if s.Name() == name {
			return s
		}
	}
	return nil
}

// normalise turns any value into what a YAML or JSON round trip gives, so a
// field read from a service and a field read from a file compare equal.
func normalise(f Fields) (Fields, error) {
	b, err := json.Marshal(f)
	if err != nil {
		return nil, err
	}
	out := Fields{}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if err := d.Decode(&out); err != nil {
		return nil, err
	}
	for k, v := range out {
		out[k] = number(v)
	}
	return out, nil
}

func number(v any) any {
	switch t := v.(type) {
	case json.Number:
		if i, err := t.Int64(); err == nil {
			return i
		}
		f, _ := t.Float64()
		return f
	case []any:
		for i := range t {
			t[i] = number(t[i])
		}
	}
	return v
}

func yamlNumbers(v any) any {
	switch t := v.(type) {
	case int:
		return int64(t)
	case uint64:
		return int64(t)
	case []any:
		for i := range t {
			t[i] = yamlNumbers(t[i])
		}
		return t
	}
	return v
}

// Export writes the whole setup as one YAML file. It holds no secret: the
// secrets block names each one and says where it lives and whether it is set.
func (e *Engine) Export(ctx context.Context) ([]byte, error) {
	root := &yaml.Node{Kind: yaml.MappingNode}
	add := func(key string, v any) error {
		var val yaml.Node
		if err := val.Encode(v); err != nil {
			return err
		}
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, &val)
		return nil
	}
	if err := add("format", FormatName); err != nil {
		return nil, err
	}
	if err := add("version", FormatVersion); err != nil {
		return nil, err
	}
	if e.Version != "" {
		if err := add("exported_by", e.Version); err != nil {
			return nil, err
		}
	}
	for _, s := range e.Sections {
		items, err := s.Read(ctx)
		if err != nil {
			return nil, fmt.Errorf("configio: read %s: %w", s.Name(), err)
		}
		if s.Singleton() {
			f, err := normalise(items[""])
			if err != nil {
				return nil, err
			}
			if err := add(s.Name(), f); err != nil {
				return nil, err
			}
			continue
		}
		names := sortedKeys(items)
		list := make([]Fields, 0, len(names))
		for _, n := range names {
			f, err := normalise(items[n])
			if err != nil {
				return nil, err
			}
			f["name"] = n
			list = append(list, f)
		}
		if err := add(s.Name(), list); err != nil {
			return nil, err
		}
	}
	refs := []SecretRef{}
	if e.Secrets != nil {
		refs = e.Secrets(ctx)
	}
	if err := add("secrets", refs); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	buf.WriteString("# crsp configuration. Plan it with `crsp config plan`; it holds no secret, only references to them.\n")
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(root); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// secretKey matches a field name that would hold a secret value.
var secretKey = regexp.MustCompile(`(?i)(^|_)(token|secret|password|passwd|private_?key|api_?key|signing_?key|credential)s?($|_)`)

// parsed is a file read and checked.
type parsed struct {
	items    map[string]map[string]Fields // section -> name -> fields
	has      map[string]bool
	problems []string
}

func (e *Engine) parse(file []byte) (parsed, error) {
	p := parsed{items: map[string]map[string]Fields{}, has: map[string]bool{}}
	var raw map[string]any
	if err := yaml.Unmarshal(file, &raw); err != nil {
		return p, fmt.Errorf("%w: %v", ErrBadFile, err)
	}
	if raw["format"] != FormatName {
		return p, fmt.Errorf("%w: the first key must be format: %s", ErrBadFile, FormatName)
	}
	ver, _ := raw["version"].(int)
	if ver < 1 || ver > FormatVersion {
		return p, fmt.Errorf("%w: version %v is not one this controller reads (it reads 1 to %d)", ErrBadFile, raw["version"], FormatVersion)
	}
	known := map[string]bool{"format": true, "version": true, "exported_by": true, "secrets": true}
	for _, s := range e.Sections {
		known[s.Name()] = true
	}
	var unknown []string
	for k := range raw {
		if !known[k] {
			unknown = append(unknown, k)
		}
	}
	sort.Strings(unknown)
	for _, k := range unknown {
		p.problems = append(p.problems, fmt.Sprintf("%s: not a section of this controller", k))
	}
	// A secrets block holds references only: a name, a ref and a state.
	if list, ok := raw["secrets"].([]any); ok {
		for i, it := range list {
			m, _ := it.(map[string]any)
			for k := range m {
				if k != "name" && k != "ref" && k != "state" {
					p.problems = append(p.problems, fmt.Sprintf("secrets[%d].%s: a secret is named by reference only (name, ref, state)", i, k))
				}
			}
		}
	}
	for _, s := range e.Sections {
		v, ok := raw[s.Name()]
		if !ok {
			continue
		}
		p.has[s.Name()] = true
		p.items[s.Name()] = map[string]Fields{}
		if s.Singleton() {
			m, ok := v.(map[string]any)
			if !ok {
				p.problems = append(p.problems, fmt.Sprintf("%s: must be a set of fields", s.Name()))
				continue
			}
			p.items[s.Name()][""] = p.fields(s.Name(), "", m)
			continue
		}
		list, ok := v.([]any)
		if !ok {
			p.problems = append(p.problems, fmt.Sprintf("%s: must be a list", s.Name()))
			continue
		}
		for i, it := range list {
			m, ok := it.(map[string]any)
			name, _ := m["name"].(string)
			if !ok || name == "" {
				p.problems = append(p.problems, fmt.Sprintf("%s[%d]: needs a name", s.Name(), i))
				continue
			}
			if _, dup := p.items[s.Name()][name]; dup {
				p.problems = append(p.problems, fmt.Sprintf("%s/%s: listed twice", s.Name(), name))
				continue
			}
			delete(m, "name")
			p.items[s.Name()][name] = p.fields(s.Name(), name, m)
		}
	}
	return p, nil
}

func (p *parsed) fields(section, name string, m map[string]any) Fields {
	f := Fields{}
	ref := section
	if name != "" {
		ref += "/" + name
	}
	for k, v := range m {
		if secretKey.MatchString(k) {
			p.problems = append(p.problems, fmt.Sprintf("%s.%s: a secret value does not belong in this file; keep it in the secret store", ref, k))
			continue
		}
		f[k] = yamlNumbers(v)
	}
	nf, err := normalise(f)
	if err != nil {
		p.problems = append(p.problems, fmt.Sprintf("%s: %v", ref, err))
		return f
	}
	return nf
}

// state reads every section as the engine compares it.
func (e *Engine) state(ctx context.Context) (map[string]map[string]Fields, string, error) {
	out := map[string]map[string]Fields{}
	h := sha256.New()
	for _, s := range e.Sections {
		items, err := s.Read(ctx)
		if err != nil {
			return nil, "", fmt.Errorf("configio: read %s: %w", s.Name(), err)
		}
		norm := map[string]Fields{}
		for n, f := range items {
			nf, err := normalise(f)
			if err != nil {
				return nil, "", err
			}
			norm[n] = nf
		}
		out[s.Name()] = norm
		b, _ := json.Marshal(norm) // map keys are sorted
		h.Write([]byte(s.Name()))
		h.Write(b)
	}
	return out, hex.EncodeToString(h.Sum(nil)), nil
}

// Plan checks the file and lists each change. It changes nothing. The plan ID
// ties the file to the state it was made against (operations R5.29).
func (e *Engine) Plan(ctx context.Context, file []byte) (Plan, error) {
	p, err := e.parse(file)
	if err != nil {
		return Plan{}, err
	}
	cur, stateHash, err := e.state(ctx)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{Changes: []Change{}, CreatedAt: e.now().UTC(), Problems: p.problems}
	for _, s := range e.Sections {
		if !p.has[s.Name()] {
			continue
		}
		want, have := p.items[s.Name()], cur[s.Name()]
		for _, n := range sortedKeys(want) {
			old, exists := have[n]
			ref := Change{Section: s.Name(), Name: n}
			if !exists {
				if !s.CanAdd() {
					plan.Problems = append(plan.Problems, fmt.Sprintf("%s: cannot be added from a file", ref.Ref()))
					continue
				}
				ref.Kind = Added
				for _, k := range sortedKeys(want[n]) {
					ref.Fields = append(ref.Fields, FieldChange{Field: k, New: want[n][k]})
				}
				plan.Changes = append(plan.Changes, ref)
				continue
			}
			var diffs []FieldChange
			for _, k := range sortedKeys(want[n]) {
				ov, had := old[k]
				if !had {
					plan.Problems = append(plan.Problems, fmt.Sprintf("%s.%s: not a field of this setup", ref.Ref(), k))
					continue
				}
				if !reflect.DeepEqual(ov, want[n][k]) {
					diffs = append(diffs, FieldChange{Field: k, Old: ov, New: want[n][k]})
				}
			}
			if len(diffs) > 0 {
				ref.Kind, ref.Fields = Changed, diffs
				plan.Changes = append(plan.Changes, ref)
			}
		}
		if !s.Singleton() {
			for _, n := range sortedKeys(have) {
				if _, in := want[n]; in {
					continue
				}
				if !s.CanRemove() {
					continue // a machine or repository left out of the file is left alone
				}
				plan.Changes = append(plan.Changes, Change{Section: s.Name(), Name: n, Kind: Removed})
			}
		}
	}
	for _, c := range plan.Changes {
		if c.Kind == Removed {
			plan.Removals = append(plan.Removals, c.Ref())
		}
	}
	if len(plan.Problems) > 0 {
		return plan, nil
	}
	sum := sha256.Sum256(append([]byte(stateHash+"\x00"), file...))
	plan.ID = "plan-" + hex.EncodeToString(sum[:])[:16]
	e.mu.Lock()
	if e.plans == nil {
		e.plans = map[string]stored{}
	}
	e.plans[plan.ID] = stored{plan: plan, state: stateHash, file: file}
	e.mu.Unlock()
	return plan, nil
}

// Apply applies exactly the plan. It refuses when the setup changed since
// the plan, and when a removal is not named in confirm. Each change writes an
// audit row. It stops at the first failure and says what was applied so far.
func (e *Engine) Apply(ctx context.Context, actor audit.Actor, planID string, confirm []string) (Result, error) {
	e.mu.Lock()
	st, ok := e.plans[planID]
	e.mu.Unlock()
	if !ok {
		return Result{}, ErrUnknownPlan
	}
	_, now, err := e.state(ctx)
	if err != nil {
		return Result{}, err
	}
	if now != st.state {
		return Result{}, ErrStale
	}
	yes := map[string]bool{}
	for _, c := range confirm {
		yes[c] = true
	}
	var missing []string
	for _, r := range st.plan.Removals {
		if !yes[r] {
			missing = append(missing, r)
		}
	}
	if len(missing) > 0 {
		return Result{}, &NeedsConfirmation{Removals: missing}
	}
	p, err := e.parse(st.file)
	if err != nil {
		return Result{}, err
	}
	res := Result{PlanID: planID, Applied: []Change{}}
	for _, c := range st.plan.Changes {
		s := e.section(c.Section)
		if s == nil {
			return res, fmt.Errorf("configio: no section %s", c.Section)
		}
		var err error
		switch c.Kind {
		case Removed:
			err = s.Delete(ctx, c.Name)
		case Added:
			err = s.Write(ctx, c.Name, p.items[c.Section][c.Name], nil)
		default:
			cur, _, rerr := e.state(ctx)
			if rerr != nil {
				return res, rerr
			}
			err = s.Write(ctx, c.Name, p.items[c.Section][c.Name], cur[c.Section][c.Name])
		}
		if err != nil {
			if e.Audit != nil {
				_ = e.Audit.RecordRefused(ctx, actor, "config.apply", audit.Target{Type: c.Section, ID: c.Name}, details(planID, c, err))
			}
			return res, fmt.Errorf("%s: %w", c.Ref(), err)
		}
		if e.Audit != nil {
			if aerr := e.Audit.Record(ctx, actor, "config.apply", audit.Target{Type: c.Section, ID: c.Name}, details(planID, c, nil)); aerr != nil {
				return res, aerr
			}
		}
		res.Applied = append(res.Applied, c)
	}
	e.mu.Lock()
	delete(e.plans, planID)
	e.mu.Unlock()
	return res, nil
}

// details is the audit detail of one change: field names and values. A
// section never returns a secret, and the audit log redacts a secret-looking
// key as a second guard.
func details(planID string, c Change, err error) map[string]any {
	d := map[string]any{"plan": planID, "kind": string(c.Kind)}
	for _, f := range c.Fields {
		d[f.Field] = fmt.Sprintf("%v -> %v", f.Old, f.New)
	}
	if err != nil {
		d["error"] = err.Error()
	}
	return d
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
