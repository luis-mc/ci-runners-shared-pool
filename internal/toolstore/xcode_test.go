package toolstore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeXcodes is a Mac with the given Xcode versions in /Applications, each named Xcode-<version>.app. A
// version that starts with "!" is an app that cannot say its version.
func fakeXcodes(versions ...string) XcodeFinder {
	var apps []string
	byDev := map[string]string{}
	for _, v := range versions {
		bad := strings.HasPrefix(v, "!")
		v = strings.TrimPrefix(v, "!")
		app := "/Applications/Xcode-" + v + ".app"
		apps = append(apps, app)
		if !bad {
			byDev[filepath.Join(app, "Contents", "Developer")] = "Xcode " + v + "\nBuild version 16C5032a\n"
		}
	}
	return XcodeFinder{
		Glob: func(p string) ([]string, error) {
			if p != XcodePattern {
				return nil, errors.New("unexpected pattern " + p)
			}
			return apps, nil
		},
		Version: func(_ context.Context, dev string) (string, error) {
			if out, ok := byDev[dev]; ok {
				return out, nil
			}
			return "", errors.New("xcodebuild: not usable")
		},
	}
}

// R5.112 (owner decision 22 Nov): the store lists the Xcodes the owner installed and picks the one a job needs.
func TestXcodeFinderListsAndSelects(t *testing.T) {
	f := fakeXcodes("15.4", "16.2", "!16.0", "16.10")
	var got []string
	for _, x := range f.List(context.Background()) {
		got = append(got, x.Version)
	}
	if strings.Join(got, ",") != "16.10,16.2,15.4" {
		t.Fatalf("list = %v (newest first, the unreadable app left out)", got)
	}
	for _, c := range []struct{ want, app string }{
		{"16.2", "/Applications/Xcode-16.2.app"}, {"16.2.0", "/Applications/Xcode-16.2.app"},
		{"15", "/Applications/Xcode-15.4.app"}, {"16", "/Applications/Xcode-16.10.app"}, {"v15.4", "/Applications/Xcode-15.4.app"},
	} {
		x, err := f.Find(context.Background(), c.want)
		if err != nil || x.App != c.app {
			t.Errorf("find %s: %+v %v", c.want, x, err)
		}
	}
	if _, err := f.Find(context.Background(), "1"); !errors.Is(err, ErrXcodeMissing) {
		t.Errorf("1 must not match 15.4: %v", err)
	}
	if v := ParseXcodeVersion("Xcode 16.2\nBuild version 16C5032a\n"); v != "16.2" || ParseXcodeVersion("garbage") != "" {
		t.Errorf("parse: %q", v)
	}
}

// A missing Xcode fails the job with a reason that names which Xcode to install, never a download; the
// doctor probe carries the same words.
func TestXcodeMissingFailsWithReasonAndDoctorHint(t *testing.T) {
	s, in := newStore(t), &FakeInstaller{}
	s.Xcodes = fakeXcodes("15.4")
	x := Tool{Tool: ToolXcode, Version: "16.2"}
	err := s.EnsureInstalled(context.Background(), in, Plan{x})
	if err == nil || !strings.Contains(err.Error(), "Install Xcode 16.2 in /Applications") || !strings.Contains(err.Error(), "installed: 15.4") {
		t.Fatalf("error = %v", err)
	}
	if in.Calls != 0 {
		t.Fatal("the installer was asked for Xcode")
	}
	if _, err := s.BuildJobHome(t.TempDir(), "j1", Plan{x}, nil); !errors.Is(err, ErrNeedsInstall) {
		t.Fatalf("job home: %v", err)
	}
	items := s.Probe(Plan{x, {Tool: ToolXcode, Version: "15.4"}})
	if items[0].Present || !strings.Contains(items[0].Hint, "Install Xcode 16.2") || !items[1].Present || items[1].Hint != "" {
		t.Fatalf("probe = %+v", items)
	}
	if l := Lacks(items); len(l) != 1 || l[0].Hint == "" {
		t.Fatalf("lacks = %+v", l)
	}
}

// The Xcode is selected in the job's own environment; nothing is written into the store and the global
// selection is never touched.
func TestXcodeSelectedThroughJobEnvironment(t *testing.T) {
	s := newStore(t)
	s.Xcodes = fakeXcodes("15.4", "16.2")
	plan := Plan{{Tool: ToolXcode, Version: "16.2"}}
	if err := s.EnsureInstalled(context.Background(), &FakeInstaller{}, plan); err != nil {
		t.Fatal(err)
	}
	jh, err := s.BuildJobHome(t.TempDir(), "j1", plan, map[string]string{"DEVELOPER_DIR": "/Applications/Xcode-15.4.app/Contents/Developer"})
	if err != nil {
		t.Fatal(err)
	}
	if got := envMap(jh.Env)["DEVELOPER_DIR"]; got != "/Applications/Xcode-16.2.app/Contents/Developer" {
		t.Fatalf("DEVELOPER_DIR = %q", got)
	}
	if _, err := os.Stat(filepath.Join(s.Root, ToolXcode)); err == nil {
		t.Fatal("the store holds an Xcode folder")
	}
}
