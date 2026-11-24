package listenaddr

import (
	"context"
	"net/netip"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/internal/settings"
	"github.com/luis-mc/ci-runners-shared-pool/internal/store"
)

type fakeSource []Iface

func (f fakeSource) Interfaces() ([]Iface, error) { return f, nil }

func a(s string) netip.Addr { return netip.MustParseAddr(s) }

var pcMachine = fakeSource{
	{Name: "Loopback Pseudo-Interface 1", Up: true, Loopback: true, Addrs: []netip.Addr{a("127.0.0.1")}},
	{Name: "vEthernet (Default Switch)", Up: true, Addrs: []netip.Addr{a("172.20.64.1")}},
	{Name: "vEthernet (WSL)", Up: true, Addrs: []netip.Addr{a("172.28.0.1")}},
	{Name: "Ethernet 2", Up: false, Addrs: []netip.Addr{a("10.9.9.9")}},
	{Name: "VMware Network Adapter VMnet1", Up: true, Addrs: []netip.Addr{a("192.168.80.1")}},
	{Name: "Ethernet", Up: true, Addrs: []netip.Addr{a("fe80::1"), a("169.254.3.4"), a("192.168.1.20")}},
}

func TestCandidatesFindThePCAndDockerAddresses(t *testing.T) {
	got, err := Candidates(pcMachine, DefaultPort)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].ID != ChoicePC || got[1].ID != ChoiceDocker || got[2].ID != ChoiceOther {
		t.Fatalf("%+v", got)
	}
	if got[0].Address != "192.168.1.20:8460" || !got[0].Recommended {
		t.Fatalf("pc: %+v", got[0])
	}
	if got[1].Address != "172.28.0.1:8460" {
		t.Fatalf("the WSL host address must win over the Default Switch: %+v", got[1])
	}
	if got[2].Address != "" || got[2].Who == "" {
		t.Fatalf("typed: %+v", got[2])
	}
}

func TestCandidatesWithNoNetworkOfferOnlyTheTypedChoice(t *testing.T) {
	got, _ := Candidates(fakeSource{{Name: "lo", Up: true, Loopback: true, Addrs: []netip.Addr{a("127.0.0.1")}}}, "8460")
	if len(got) != 1 || got[0].ID != ChoiceOther {
		t.Fatalf("%+v", got)
	}
}

func TestValidate(t *testing.T) {
	for _, c := range []struct{ in, want, bad string }{
		{"192.168.1.20:9000", "192.168.1.20:9000", ""},
		{"192.168.1.20", "192.168.1.20:8460", ""},
		{"0.0.0.0:8460", "0.0.0.0:8460", ""},
		{"", "", "host:port"},
		{"nope.lan:8460", "", "not an IP address"},
		{"192.168.1.20:99999", "", "port"},
		{"127.0.0.1:8460", "", "loopback"},
		{"10.1.1.1:8460", "", "not an address of this PC"},
	} {
		got, err := Validate(pcMachine, c.in)
		if c.bad == "" && (err != nil || got != c.want) {
			t.Errorf("%q: %q %v", c.in, got, err)
		}
		if c.bad != "" && (err == nil || !strings.Contains(err.Error(), c.bad)) {
			t.Errorf("%q: want %q, got %v", c.in, c.bad, err)
		}
	}
}

func TestEffectiveEnvironmentWinsOverStored(t *testing.T) {
	for _, c := range []struct {
		env, stored, conf, want string
		from                    bool
	}{
		{"", "", "127.0.0.1:8460", "127.0.0.1:8460", false},
		{"", "192.168.1.20:8460", "127.0.0.1:8460", "192.168.1.20:8460", true},
		{"10.0.0.2:8460", "192.168.1.20:8460", "10.0.0.2:8460", "10.0.0.2:8460", false},
	} {
		got, from := Effective(c.env, c.stored, c.conf)
		if got != c.want || from != c.from {
			t.Errorf("%+v: %q %v", c, got, from)
		}
	}
}

func newService(t *testing.T, env string) (*Service, *audit.Log) {
	t.Helper()
	db, err := store.OpenForTests(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &Service{Settings: settings.New(db), Source: pcMachine, Agent: "127.0.0.1:8461",
		Getenv: func(k string) string {
			if k == EnvName {
				return env
			}
			return ""
		}}, audit.New(db)
}

func TestChoosePersistsAndAudits(t *testing.T) {
	ctx := context.Background()
	s, log := newService(t, "")
	who := audit.Actor{Login: "octo", ID: "1", Source: audit.SourceWeb}
	addr, err := s.Choose(ctx, who, "192.168.1.20:8460")
	if err != nil || addr != "192.168.1.20:8460" {
		t.Fatalf("%q %v", addr, err)
	}
	st, err := s.State(ctx)
	if err != nil || st.Stored != "192.168.1.20:8460" || st.FromEnv || len(st.Candidates) != 3 {
		t.Fatalf("%+v %v", st, err)
	}
	page, err := log.List(ctx, audit.Filter{})
	if err != nil || len(page.Items) != 1 || page.Items[0].Action != settings.ActionListenSet || page.Items[0].Actor != "octo" {
		t.Fatalf("%+v %v", page, err)
	}
}

func TestChooseRefusals(t *testing.T) {
	ctx := context.Background()
	who := audit.Actor{Login: "octo", Source: audit.SourceWeb}
	s, log := newService(t, "")
	if _, err := s.Choose(ctx, who, "192.168.1.20:8461"); err == nil || !strings.Contains(err.Error(), "agent link") {
		t.Fatalf("the agent port: %v", err)
	}
	var bad *ErrInvalid
	if _, err := s.Choose(ctx, who, "nope"); !asInvalid(err, &bad) {
		t.Fatalf("%v", err)
	}
	e, _ := newService(t, "10.0.0.2:8460")
	if _, err := e.Choose(ctx, who, "192.168.1.20:8460"); err != ErrFromEnv {
		t.Fatalf("env wins: %v", err)
	}
	if st, _ := e.State(ctx); !st.FromEnv || st.EnvName != EnvName || st.Stored != "" {
		t.Fatalf("%+v", st)
	}
	if page, _ := log.List(ctx, audit.Filter{}); len(page.Items) != 0 {
		t.Fatalf("a refusal must store and audit nothing: %+v", page.Items)
	}
}

func asInvalid(err error, target **ErrInvalid) bool {
	e, ok := err.(*ErrInvalid)
	*target = e
	return ok
}
