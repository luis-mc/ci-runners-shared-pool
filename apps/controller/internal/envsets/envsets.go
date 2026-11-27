// Package envsets holds named environment-variable sets and merges them for a
// runner (runners R5.75). A set such as "dotnet-8" is stored once and applied
// to many runners. The merged result has a fixed order: machine values first,
// then each applied set in the order the runner lists them, then the runner's
// own values. A later layer wins.
//
// A value flagged sensitive is kept only in the OS secret store, never in the
// set's metadata, and is masked in every view and log (runners R5.76, R6.2).
package envsets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/redact"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/secrets"
)

// Mask is what a view shows in place of a sensitive value.
const Mask = redact.Mask

// Layer names say where a merged value came from.
const (
	SourceMachine = "machine"
	SourceRunner  = "runner"
	// A value from a set has the source "set:<name>".
	sourceSetPrefix = "set:"
)

var (
	// ErrNotFound is returned for a set that does not exist.
	ErrNotFound = errors.New("env set not found")
	// ErrInvalid wraps every refusal of a name or value.
	ErrInvalid = errors.New("invalid env set")

	setName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)
	varName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
)

// reserved prefixes belong to the Actions runner and to crsp; a set cannot
// override them (runners R5.53, R6.2).
var reservedPrefixes = []string{"GITHUB_", "ACTIONS_RUNNER_", "RUNNER_", "CRSP_"}

// Var is one variable of a set.
type Var struct {
	Name      string `json:"name"`
	Value     string `json:"value,omitempty"` // empty and never stored when Sensitive
	Sensitive bool   `json:"sensitive,omitempty"`
}

// Set is a named group of variables.
type Set struct {
	Name string `json:"name"`
	Vars []Var  `json:"vars"`
}

// Resolved is one value of a merged environment.
type Resolved struct {
	Name      string
	Value     string
	Sensitive bool
	Source    string
}

// Meta persists set metadata: names, variable names, flags and non-sensitive
// values. It never receives a sensitive value.
type Meta interface {
	Load(ctx context.Context) (map[string][]byte, error)
	Save(ctx context.Context, name string, data []byte) error
	Remove(ctx context.Context, name string) error
}

// MemoryMeta is an in-process Meta for tests and the first wiring.
type MemoryMeta struct {
	mu sync.Mutex
	m  map[string][]byte
}

// Load implements Meta.
func (m *MemoryMeta) Load(context.Context) (map[string][]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string][]byte, len(m.m))
	for k, v := range m.m {
		out[k] = append([]byte(nil), v...)
	}
	return out, nil
}

// Save implements Meta.
func (m *MemoryMeta) Save(_ context.Context, name string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.m == nil {
		m.m = map[string][]byte{}
	}
	m.m[name] = append([]byte(nil), data...)
	return nil
}

// Remove implements Meta.
func (m *MemoryMeta) Remove(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.m, name)
	return nil
}

// Registry stores sets. Sensitive values go to the secret store, which
// encrypts at rest (decision 0002); the rest goes to Meta.
type Registry struct {
	Meta    Meta
	Secrets secrets.Store
	// Redactor learns every sensitive value that passes through, so a log line
	// that carries one is masked.
	Redactor *redact.Redactor
}

func secretKey(set, name string) secrets.Key {
	return secrets.Key("crsp/envset/" + set + "/" + name)
}

// ValidateSet checks names and refuses reserved ones and duplicates.
func ValidateSet(s Set) error {
	if !setName.MatchString(s.Name) {
		return fmt.Errorf("%w: set name %q: use lower case letters, digits, dot, dash or underscore", ErrInvalid, s.Name)
	}
	seen := map[string]bool{}
	for _, v := range s.Vars {
		if err := ValidateName(v.Name); err != nil {
			return err
		}
		if seen[v.Name] {
			return fmt.Errorf("%w: variable %q is listed twice", ErrInvalid, v.Name)
		}
		seen[v.Name] = true
		if strings.ContainsAny(v.Value, "\x00\r\n") {
			return fmt.Errorf("%w: the value of %s holds a line break or NUL", ErrInvalid, v.Name)
		}
	}
	return nil
}

// ValidateName checks one variable name.
func ValidateName(n string) error {
	if !varName.MatchString(n) {
		return fmt.Errorf("%w: variable name %q", ErrInvalid, n)
	}
	up := strings.ToUpper(n)
	for _, p := range reservedPrefixes {
		if strings.HasPrefix(up, p) {
			return fmt.Errorf("%w: %s is reserved (%s*)", ErrInvalid, n, p)
		}
	}
	return nil
}

// Put creates or replaces a set. A sensitive variable sent with an empty value
// keeps the stored value, so an edit does not need to send it again.
func (r *Registry) Put(ctx context.Context, s Set) error {
	if err := ValidateSet(s); err != nil {
		return err
	}
	old, oldErr := r.Get(ctx, s.Name)
	if oldErr != nil && !errors.Is(oldErr, ErrNotFound) {
		return oldErr
	}
	oldSens := map[string]bool{}
	for _, v := range old.Vars {
		if v.Sensitive {
			oldSens[v.Name] = true
		}
	}
	meta := Set{Name: s.Name}
	keep := map[string]bool{}
	for _, v := range s.Vars {
		if v.Sensitive {
			switch {
			case v.Value != "":
				if err := r.Secrets.Put(ctx, secretKey(s.Name, v.Name), []byte(v.Value)); err != nil {
					return fmt.Errorf("store sensitive value of %s: %w", v.Name, err)
				}
				r.learn(v.Value)
			case !oldSens[v.Name]:
				return fmt.Errorf("%w: sensitive variable %s needs a value", ErrInvalid, v.Name)
			}
			keep[v.Name] = true
			meta.Vars = append(meta.Vars, Var{Name: v.Name, Sensitive: true})
			continue
		}
		meta.Vars = append(meta.Vars, Var{Name: v.Name, Value: v.Value})
	}
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	if err := r.Meta.Save(ctx, s.Name, data); err != nil {
		return err
	}
	for name := range oldSens {
		if !keep[name] {
			if err := r.Secrets.Delete(ctx, secretKey(s.Name, name)); err != nil {
				return fmt.Errorf("drop stored value of %s: %w", name, err)
			}
		}
	}
	return nil
}

func (r *Registry) learn(v string) {
	if r.Redactor != nil {
		r.Redactor.Add(v)
	}
}

// Get returns a set as stored: sensitive variables carry no value.
func (r *Registry) Get(ctx context.Context, name string) (Set, error) {
	all, err := r.Meta.Load(ctx)
	if err != nil {
		return Set{}, err
	}
	data, ok := all[name]
	if !ok {
		return Set{}, ErrNotFound
	}
	var s Set
	if err := json.Unmarshal(data, &s); err != nil {
		return Set{}, fmt.Errorf("read env set %s: %w", name, err)
	}
	return s, nil
}

// List returns every set by name.
func (r *Registry) List(ctx context.Context) ([]Set, error) {
	all, err := r.Meta.Load(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(all))
	for n := range all {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]Set, 0, len(names))
	for _, n := range names {
		var s Set
		if err := json.Unmarshal(all[n], &s); err != nil {
			return nil, fmt.Errorf("read env set %s: %w", n, err)
		}
		out = append(out, s)
	}
	return out, nil
}

// Delete removes a set and its stored sensitive values.
func (r *Registry) Delete(ctx context.Context, name string) error {
	s, err := r.Get(ctx, name)
	if err != nil {
		return err
	}
	for _, v := range s.Vars {
		if v.Sensitive {
			if err := r.Secrets.Delete(ctx, secretKey(name, v.Name)); err != nil {
				return err
			}
		}
	}
	return r.Meta.Remove(ctx, name)
}

// Layers is what a merge takes: the machine's values, the set names the runner
// applies in order, and the runner's own values.
type Layers struct {
	Machine []Var
	Sets    []string
	Runner  []Var
}

// Merge resolves the environment of a runner with real values. Priority is
// machine < sets in the listed order < runner. A missing set is an error: a
// runner never starts with half of its environment. The result is sorted by
// name. Use Redacted for anything shown or logged.
func (r *Registry) Merge(ctx context.Context, l Layers) ([]Resolved, error) {
	got := map[string]Resolved{}
	put := func(v Var, source string) {
		got[v.Name] = Resolved{Name: v.Name, Value: v.Value, Sensitive: v.Sensitive, Source: source}
	}
	for _, v := range l.Machine {
		put(v, SourceMachine)
	}
	for _, name := range l.Sets {
		s, err := r.Get(ctx, name)
		if err != nil {
			return nil, fmt.Errorf("env set %q: %w", name, err)
		}
		for _, v := range s.Vars {
			if v.Sensitive {
				b, err := r.Secrets.Get(ctx, secretKey(name, v.Name))
				if err != nil {
					return nil, fmt.Errorf("env set %q, %s: %w", name, v.Name, err)
				}
				v.Value = string(b)
				r.learn(v.Value)
			}
			put(v, sourceSetPrefix+name)
		}
	}
	for _, v := range l.Runner {
		if v.Sensitive {
			r.learn(v.Value)
		}
		put(v, SourceRunner)
	}
	out := make([]Resolved, 0, len(got))
	for _, v := range got {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Redacted returns the merged result with every sensitive value masked. It is
// what `crsp runner env` and the API show.
func Redacted(in []Resolved) []Resolved {
	out := make([]Resolved, len(in))
	for i, v := range in {
		if v.Sensitive {
			v.Value = Mask
		}
		out[i] = v
	}
	return out
}

// Lines renders NAME=value lines for a runner's env file. It refuses a value
// with a line break.
func Lines(in []Resolved) ([]string, error) {
	out := make([]string, 0, len(in))
	for _, v := range in {
		if strings.ContainsAny(v.Value, "\x00\r\n") {
			return nil, fmt.Errorf("%w: the value of %s holds a line break or NUL", ErrInvalid, v.Name)
		}
		out = append(out, v.Name+"="+v.Value)
	}
	return out, nil
}

// LayerSource gives the layers of one runner: the values of its machine, the
// sets it applies in order and its own values. The runner record owns them.
type LayerSource interface {
	Layers(ctx context.Context, runnerID string) (Layers, error)
}

// Service is what the API and the CLI call: the registry plus the layers of
// each runner.
type Service struct {
	*Registry
	Source LayerSource
}

// RunnerEnv returns the merged environment of a runner with every sensitive
// value masked (runners R5.75, R5.76). The real values go only to the runner
// start, through Registry.Merge.
func (s *Service) RunnerEnv(ctx context.Context, runnerID string) ([]Resolved, error) {
	l, err := s.Source.Layers(ctx, runnerID)
	if err != nil {
		return nil, err
	}
	m, err := s.Merge(ctx, l)
	if err != nil {
		return nil, err
	}
	return Redacted(m), nil
}
