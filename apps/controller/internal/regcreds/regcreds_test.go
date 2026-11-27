package regcreds

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/redact"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/secrets"
)

const password = "hunter2-registry-pw"

func caPEM(t *testing.T) []byte {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "crsp test ca"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

type fakeFS struct {
	files map[string][]byte
	modes map[string]uint32
	dirs  []string
}

func newFS() *fakeFS { return &fakeFS{files: map[string][]byte{}, modes: map[string]uint32{}} }
func (f *fakeFS) MkdirAll(d string, _ uint32) error {
	f.dirs = append(f.dirs, d)
	return nil
}
func (f *fakeFS) WriteFile(n string, d []byte, m uint32) error {
	f.files[n] = d
	f.modes[n] = m
	return nil
}

type fakeExec struct {
	ran  []Command
	fail error
}

func (e *fakeExec) Run(_ context.Context, c Command) error {
	e.ran = append(e.ran, c)
	return e.fail
}

func newStore(t *testing.T, kinds ...string) (*Store, *secrets.Memory, *MemoryMeta) {
	t.Helper()
	sm, mm := secrets.NewMemory(), &MemoryMeta{}
	s := &Store{Meta: mm, Secrets: sm, Redactor: redact.New()}
	ctx := context.Background()
	for _, k := range kinds {
		if _, err := s.PutLogin(ctx, RegistryLogin{Kind: k, Host: "reg.example.com:5000", Username: "ci"}, password); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.PutBundle(ctx, caPEM(t)); err != nil {
		t.Fatal(err)
	}
	return s, sm, mm
}

func TestLoginPasswordOnlyInSecretStore(t *testing.T) {
	s, sm, mm := newStore(t, KindContainer)
	for n, d := range mm.m {
		if strings.Contains(string(d), password) {
			t.Fatalf("meta %s holds the password", n)
		}
	}
	ls, _ := s.Logins(context.Background(), "")
	if len(ls) != 1 || ls[0].CredentialRef == "" || len(sm.Keys()) != 1 {
		t.Fatalf("logins %+v keys %v", ls, sm.Keys())
	}
	pw, err := s.Password(context.Background(), ls[0])
	if err != nil || pw.Reveal() != password {
		t.Fatal(err)
	}
	for _, f := range []string{"%v", "%+v", "%#v", "%s"} {
		if strings.Contains(fmt.Sprintf(f, pw), password) {
			t.Fatalf("%s leaks", f)
		}
	}
	if strings.Contains(s.Redactor.Redact("x "+password), password) {
		t.Fatal("redactor does not know the password")
	}
}

func TestLoginRules(t *testing.T) {
	s, sm, _ := newStore(t)
	ctx := context.Background()
	if _, err := s.PutLogin(ctx, RegistryLogin{Kind: "vm", Host: "Bad Host", Username: "u"}, "pw-1234"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("host: %v", err)
	}
	if _, err := s.PutLogin(ctx, RegistryLogin{Kind: "vm", Host: "r.io", Username: "u"}, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("new without password: %v", err)
	}
	if _, err := s.PutLogin(ctx, RegistryLogin{Kind: "vm", Host: "r.io", Username: "u"}, "pw-1234"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutLogin(ctx, RegistryLogin{Kind: "vm", Host: "r.io", Username: "other"}, ""); err != nil {
		t.Fatalf("edit keeps password: %v", err)
	}
	if err := s.DeleteLogin(ctx, "vm", "r.io"); err != nil || len(sm.Keys()) != 0 {
		t.Fatalf("delete: %v %v", err, sm.Keys())
	}
	if err := s.DeleteLogin(ctx, "vm", "r.io"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
	sm.Down = true
	if _, err := s.PutLogin(ctx, RegistryLogin{Kind: "vm", Host: "r.io", Username: "u"}, "pw-1234"); !errors.Is(err, secrets.ErrUnavailable) {
		t.Fatalf("down: %v", err)
	}
}

func TestBundleValidation(t *testing.T) {
	good := caPEM(t)
	if _, err := ValidateBundle(good); err != nil {
		t.Fatal(err)
	}
	key := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: []byte("x")})
	for name, in := range map[string][]byte{
		"empty": nil, "text": []byte("hello"), "key": append(append([]byte{}, good...), key...),
		"junk cert": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("nope")}),
		"trailing":  append(append([]byte{}, good...), []byte("tail")...),
	} {
		if _, err := ValidateBundle(in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestContainerCAMountUpdateAndLoginOnStdin(t *testing.T) {
	ctx := context.Background()
	s, _, _ := newStore(t, KindContainer, KindVM)
	fs := newFS()
	p, err := PlanContainer(ctx, s, fs, "docker", "pool-1", "/var/crsp/ca")
	if err != nil {
		t.Fatal(err)
	}
	if p.CAMount == nil || p.CAMount.GuestDir != GuestCADir || p.CAMount.HostDir != "/var/crsp/ca" {
		t.Fatalf("mount %+v", p.CAMount)
	}
	if len(fs.files[filepath.Join("/var/crsp/ca", "crsp-private-ca.crt")]) == 0 {
		t.Fatal("bundle not written to the mounted folder")
	}
	if len(p.AfterStart) != 1 || p.AfterStart[0].Args[len(p.AfterStart[0].Args)-1] != "update-ca-certificates" {
		t.Fatalf("after start %+v", p.AfterStart)
	}
	if len(p.Logins) != 1 {
		t.Fatalf("only container logins: %+v", p.Logins)
	}
	noLeak(t, p.Logins)
	ex := &fakeExec{}
	if err := RunLogins(ctx, ex, p.Logins); err != nil || ex.ran[0].Stdin.Reveal() != password {
		t.Fatalf("login stdin: %v", err)
	}
	ex.fail = errors.New("denied")
	if err := RunLogins(ctx, ex, p.Logins); err == nil || strings.Contains(err.Error(), password) {
		t.Fatalf("error: %v", err)
	}
}

func noLeak(t *testing.T, cmds []Command) {
	t.Helper()
	for _, c := range cmds {
		all := c.Name + " " + strings.Join(c.Args, " ") + " " + strings.Join(c.Env, " ")
		if strings.Contains(all, password) {
			t.Fatalf("password in command line or env: %s", all)
		}
		if !strings.Contains(all, "--password-stdin") {
			t.Fatalf("login without --password-stdin: %s", all)
		}
	}
}

func TestVMBootInjection(t *testing.T) {
	ctx := context.Background()
	s, _, _ := newStore(t, KindVM, KindContainer)
	p, err := PlanVM(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	var ca, login *BootFile
	for i := range p.Files {
		switch {
		case p.Files[i].Path == GuestCAFile:
			ca = &p.Files[i]
		case strings.HasPrefix(p.Files[i].Path, "/run/crsp/login-"):
			login = &p.Files[i]
		}
	}
	if ca == nil || ca.Mode != 0o644 || login == nil || login.Mode != 0o600 || len(p.Files) != 2 {
		t.Fatalf("files %+v", p.Files)
	}
	cmds := strings.Join(p.Commands, "\n")
	if !strings.Contains(cmds, "update-ca-certificates") || !strings.Contains(cmds, "--password-stdin <") || !strings.Contains(cmds, "rm -f") {
		t.Fatalf("commands %s", cmds)
	}
	if strings.Contains(cmds, password) {
		t.Fatal("password in boot command")
	}
}

func TestPersistentRunnerEnvOnlyNeverHostTrustStore(t *testing.T) {
	ctx := context.Background()
	s, _, _ := newStore(t, KindPersistent)
	fs := newFS()
	sys := []byte("SYSTEM-BUNDLE")
	p, err := PlanPersistent(ctx, s, fs, "docker", "/srv/runner-1", sys)
	if err != nil {
		t.Fatal(err)
	}
	env := strings.Join(p.Env, "\n")
	for _, w := range []string{"NODE_EXTRA_CA_CERTS=" + filepath.Join("/srv/runner-1", "crsp-ca", "private-ca.pem"), "SSL_CERT_FILE=" + filepath.Join("/srv/runner-1", "crsp-ca", "combined.pem"),
		"GIT_SSL_CAINFO=", "DOCKER_CONFIG=" + filepath.Join("/srv/runner-1", "docker-config")} {
		if !strings.Contains(env, w) {
			t.Errorf("env lacks %s:\n%s", w, env)
		}
	}
	if strings.Contains(env, password) {
		t.Fatal("password in job environment")
	}
	if !strings.HasPrefix(string(fs.files[filepath.Join("/srv/runner-1", "crsp-ca", "combined.pem")]), "SYSTEM-BUNDLE") {
		t.Fatal("combined file must keep public roots")
	}
	for n := range fs.files {
		if !strings.HasPrefix(n, filepath.Join("/srv/runner-1")+string(filepath.Separator)) {
			t.Fatalf("wrote outside the runner folder: %s", n)
		}
		if strings.HasPrefix(n, "/etc/") || strings.Contains(n, "ca-certificates") {
			t.Fatalf("touched the host trust store: %s", n)
		}
	}
	noLeak(t, p.Logins)
	if p.Logins[0].Env[0] != "DOCKER_CONFIG="+filepath.Join("/srv/runner-1", "docker-config") {
		t.Fatal("login must go to the runner's own config folder")
	}
}

func TestNothingSetMeansNothingInjected(t *testing.T) {
	ctx := context.Background()
	s := &Store{Meta: &MemoryMeta{}, Secrets: secrets.NewMemory()}
	c, _ := PlanContainer(ctx, s, newFS(), "docker", "p", "/ca")
	v, _ := PlanVM(ctx, s)
	pp, _ := PlanPersistent(ctx, s, newFS(), "docker", "/r", nil)
	if c.CAMount != nil || len(c.Logins)+len(v.Files)+len(pp.Env)+len(pp.Logins) != 0 {
		t.Fatalf("%+v %+v %+v", c, v, pp)
	}
}
