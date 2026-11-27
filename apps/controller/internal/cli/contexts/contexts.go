// Package contexts reads and writes the CLI config file (cli R5.6, R5.7, R6.2).
// The file holds context names, controller URLs, pinned certificate
// fingerprints and the current context. It never holds a token.
package contexts

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Context is one controller the CLI can talk to.
type Context struct {
	Name        string
	URL         string
	Fingerprint string // SHA-256 of the controller certificate, lower-case hex
}

// File is the parsed config.
type File struct {
	Current  string
	Contexts map[string]Context
}

// ErrNoContext means no context was chosen and none is current.
var ErrNoContext = errors.New("no controller chosen")

// Path is the config file under dir.
func Path(dir string) string { return filepath.Join(dir, "cli.toml") }

// Load reads the config file in dir. A missing file is an empty config.
func Load(dir string) (*File, error) {
	f := &File{Contexts: map[string]Context{}}
	fh, err := os.Open(Path(dir))
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = fh.Close() }()
	section := ""
	sc := bufio.NewScanner(fh)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[context.") && strings.HasSuffix(line, "]") {
			name, err := strconv.Unquote(strings.TrimSuffix(strings.TrimPrefix(line, "[context."), "]"))
			if err != nil {
				return nil, fmt.Errorf("config: bad section %s", line)
			}
			f.Contexts[name] = Context{Name: name}
			section = name
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("config: bad line %q", line)
		}
		k = strings.TrimSpace(k)
		val, err := strconv.Unquote(strings.TrimSpace(v))
		if err != nil {
			return nil, fmt.Errorf("config: bad value for %s", k)
		}
		switch {
		case section == "" && k == "current":
			f.Current = val
		case section != "" && k == "url":
			c := f.Contexts[section]
			c.URL = val
			f.Contexts[section] = c
		case section != "" && k == "fingerprint":
			c := f.Contexts[section]
			c.Fingerprint = val
			f.Contexts[section] = c
		}
	}
	return f, sc.Err()
}

// Save writes the config file in dir (mode 0600, directory 0700).
func (f *File) Save(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("# crsp CLI contexts. No tokens are kept here.\n")
	fmt.Fprintf(&b, "current = %s\n", strconv.Quote(f.Current))
	for _, name := range f.Names() {
		c := f.Contexts[name]
		fmt.Fprintf(&b, "\n[context.%s]\nurl = %s\nfingerprint = %s\n", strconv.Quote(name), strconv.Quote(c.URL), strconv.Quote(c.Fingerprint))
	}
	return os.WriteFile(Path(dir), []byte(b.String()), 0o600)
}

// Names lists context names in order.
func (f *File) Names() []string {
	names := make([]string, 0, len(f.Contexts))
	for n := range f.Contexts {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Set adds or replaces a context and makes it current (R5.6).
func (f *File) Set(c Context) {
	f.Contexts[c.Name] = c
	f.Current = c.Name
}

// Use makes an existing context current.
func (f *File) Use(name string) error {
	if _, ok := f.Contexts[name]; !ok {
		return fmt.Errorf("no context named %q", name)
	}
	f.Current = name
	return nil
}

// Remove deletes a context. The caller deletes its token.
func (f *File) Remove(name string) error {
	if _, ok := f.Contexts[name]; !ok {
		return fmt.Errorf("no context named %q", name)
	}
	delete(f.Contexts, name)
	if f.Current == name {
		f.Current = ""
	}
	return nil
}

// Pick chooses the context in the order of R5.7: the flag, then the
// environment variable, then the current context.
func (f *File) Pick(flagName, envName string) (Context, error) {
	name := flagName
	if name == "" {
		name = envName
	}
	if name == "" {
		name = f.Current
	}
	if name == "" {
		return Context{}, ErrNoContext
	}
	c, ok := f.Contexts[name]
	if !ok {
		return Context{}, fmt.Errorf("no context named %q", name)
	}
	return c, nil
}

// ByURL finds the context that pins url, for --controller (R5.7).
func (f *File) ByURL(url string) (Context, bool) {
	url = strings.TrimRight(url, "/")
	for _, n := range f.Names() {
		if strings.TrimRight(f.Contexts[n].URL, "/") == url {
			return f.Contexts[n], true
		}
	}
	return Context{}, false
}
