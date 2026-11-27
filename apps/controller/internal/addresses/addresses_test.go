package addresses_test

import (
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/addresses"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestLoadDefaults(t *testing.T) { // OPS35
	a, err := addresses.Load(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	want := addresses.Set{GitHubURL: "https://github.com", GitHubAPIURL: "https://api.github.com",
		RunnerDownloadURL: "https://github.com/actions/runner/releases/download"}
	if a != want {
		t.Fatalf("got %+v, want %+v", a, want)
	}
	if addresses.Defaults() != want {
		t.Fatal("Defaults differs from an empty environment")
	}
}

func TestLoadEnterpriseDerivesAPIAndDownload(t *testing.T) { // OPS35
	a, err := addresses.Load(env(map[string]string{addresses.EnvGitHubURL: "https://ghe.example.com"}))
	if err != nil {
		t.Fatal(err)
	}
	if a.GitHubAPIURL != "https://ghe.example.com/api/v3" {
		t.Fatalf("api = %q", a.GitHubAPIURL)
	}
	if a.RunnerDownloadURL != "https://ghe.example.com/actions/runner/releases/download" {
		t.Fatalf("download = %q", a.RunnerDownloadURL)
	}
}

func TestLoadExplicitValuesWin(t *testing.T) { // OPS35
	a, err := addresses.Load(env(map[string]string{
		addresses.EnvGitHubURL:         "https://ghe.example.com",
		addresses.EnvGitHubAPIURL:      "https://api.ghe.example.com",
		addresses.EnvRunnerDownloadURL: "http://127.0.0.1:9000/runner",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if a.GitHubAPIURL != "https://api.ghe.example.com" || a.RunnerDownloadURL != "http://127.0.0.1:9000/runner" {
		t.Fatalf("got %+v", a)
	}
}

func TestLoadRejectsBadAddressesNamingTheVariable(t *testing.T) { // OPS35
	for _, c := range []struct{ name, value string }{
		{addresses.EnvGitHubURL, "github.com"},
		{addresses.EnvGitHubURL, "https://github.com/"},
		{addresses.EnvGitHubAPIURL, "http://api.example.com"},
		{addresses.EnvRunnerDownloadURL, "ftp://example.com"},
		{addresses.EnvGitHubURL, "https://user:pw@example.com"},
		{addresses.EnvGitHubURL, "https://example.com?x=1"},
	} {
		_, err := addresses.Load(env(map[string]string{c.name: c.value}))
		if err == nil || !strings.Contains(err.Error(), c.name) {
			t.Errorf("%s=%q: err = %v, want one naming the variable", c.name, c.value, err)
		}
	}
	for _, ok := range []string{"http://localhost:8080", "http://127.0.0.1"} {
		if _, err := addresses.Load(env(map[string]string{addresses.EnvGitHubURL: ok})); err != nil {
			t.Errorf("%q should pass: %v", ok, err)
		}
	}
}

func TestLoadReportsEveryProblem(t *testing.T) { // OPS35
	_, err := addresses.Load(env(map[string]string{addresses.EnvGitHubURL: "nope", addresses.EnvGitHubAPIURL: "nope"}))
	if err == nil || !strings.Contains(err.Error(), addresses.EnvGitHubURL) || !strings.Contains(err.Error(), addresses.EnvGitHubAPIURL) {
		t.Fatalf("err = %v", err)
	}
}

// A GitHub Enterprise Server host: the runner download and the grant pages
// follow it and never name github.com.
func TestEnterpriseHostRunnerURLAndGrantPages(t *testing.T) { // OPS35
	a, err := addresses.Load(env(map[string]string{addresses.EnvGitHubURL: "https://ghe.example.com"}))
	if err != nil {
		t.Fatal(err)
	}
	got := runners.Pin{OS: "linux", Arch: "x64", Version: "2.330.0"}.DownloadURL(a)
	want := "https://ghe.example.com/actions/runner/releases/download/v2.330.0/actions-runner-linux-x64-2.330.0.tar.gz"
	if got != want {
		t.Fatalf("runner url = %q, want %q", got, want)
	}
	appPage, insPage := github.GrantPages(a.GitHubURL, github.App{Slug: "crsp", OwnerType: "Organization", OwnerLogin: "acme"},
		github.Installation{ID: 7, AccountType: "Organization", AccountLogin: "acme"})
	if appPage != "https://ghe.example.com/organizations/acme/settings/apps/crsp/permissions" ||
		insPage != "https://ghe.example.com/organizations/acme/settings/installations/7" {
		t.Fatalf("pages = %q, %q", appPage, insPage)
	}
	if got := a.InstallPage("crsp"); got != "https://ghe.example.com/apps/crsp/installations/new" {
		t.Fatalf("install page = %q", got)
	}
	if got := a.JobPage("acme/app", 5, 6); got != "https://ghe.example.com/acme/app/actions/runs/5/job/6" {
		t.Fatalf("job page = %q", got)
	}
	if got := a.RepoPage("acme/app"); got != "https://ghe.example.com/acme/app" {
		t.Fatalf("repo page = %q", got)
	}
}

func TestZeroSetDownloadURLUsesDefaults(t *testing.T) { // OPS35
	got := runners.Pin{OS: "windows", Arch: "x64", Version: "2.330.0"}.DownloadURL(addresses.Set{})
	if !strings.HasPrefix(got, "https://github.com/actions/runner/releases/download/v2.330.0/actions-runner-win-x64-2.330.0.zip") {
		t.Fatalf("got %q", got)
	}
}

func TestVMImageMirrorDefaultIsEmptyAndUnchanged(t *testing.T) { // OPS37
	a, err := addresses.Load(env(nil))
	if err != nil || a.VMImageMirror != "" || addresses.DefaultVMImageMirror != "" {
		t.Fatalf("%+v %v", a, err)
	}
	up := "https://dl.rockylinux.org/pub/rocky/9/images/x86_64/"
	if got := a.VMImageURL("rocky-9", up); got != up {
		t.Fatalf("default changed the address: %q", got)
	}
}

func TestVMImageMirrorIsReadValidatedAndUsedAsTheBase(t *testing.T) { // OPS37
	a, err := addresses.Load(env(map[string]string{"CRSP_VM_IMAGE_MIRROR": "https://mirror.corp/vm"}))
	if err != nil || a.VMImageMirror != "https://mirror.corp/vm" {
		t.Fatalf("%+v %v", a, err)
	}
	if got := a.VMImageURL("rocky-9", "https://h/pub/rocky/9/"); got != "https://mirror.corp/vm/rocky-9/" {
		t.Errorf("directory: %q", got)
	}
	if got := a.VMImageURL("debian-12", "https://h/x/SHA512SUMS"); got != "https://mirror.corp/vm/debian-12/SHA512SUMS" {
		t.Errorf("file: %q", got)
	}
	for _, bad := range []string{"mirror.corp", "http://mirror.corp/vm", "https://mirror.corp/vm/"} {
		if _, err := addresses.Load(env(map[string]string{"CRSP_VM_IMAGE_MIRROR": bad})); err == nil || !strings.Contains(err.Error(), "CRSP_VM_IMAGE_MIRROR") {
			t.Errorf("%q: %v", bad, err)
		}
		if _, err := addresses.VMImageMirrorFrom(env(map[string]string{"CRSP_VM_IMAGE_MIRROR": bad})); err == nil {
			t.Errorf("%q: VMImageMirrorFrom accepted it", bad)
		}
	}
}
