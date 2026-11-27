// Package listenaddr is the controller's listen address as the console sets it
// (operations R5.90): the addresses of this machine to offer, the check of a
// typed address, and the stored choice with CRSP_LISTEN_ADDR winning over it.
package listenaddr

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/settings"
)

// DefaultPort is the console port unless the owner types another.
const DefaultPort = "8460"

// Choice ids.
const (
	ChoicePC     = "pc"
	ChoiceDocker = "docker"
	ChoiceOther  = "other"
)

// Iface is one network interface of this machine.
type Iface struct {
	Name     string
	Up       bool
	Loopback bool
	Addrs    []netip.Addr
}

// Source lists the interfaces of this machine.
type Source interface {
	Interfaces() ([]Iface, error)
}

// SystemSource reads the real interfaces.
type SystemSource struct{}

// Interfaces implements Source.
func (SystemSource) Interfaces() ([]Iface, error) {
	all, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	out := make([]Iface, 0, len(all))
	for _, i := range all {
		f := Iface{Name: i.Name, Up: i.Flags&net.FlagUp != 0, Loopback: i.Flags&net.FlagLoopback != 0}
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if p, ok := netip.AddrFromSlice(ip); ok {
				f.Addrs = append(f.Addrs, p.Unmap())
			}
		}
		out = append(out, f)
	}
	return out, nil
}

// Candidate is one address the console offers.
type Candidate struct {
	ID          string
	Label       string
	Address     string // host:port; empty for the typed choice
	Who         string // who can reach it, one line
	Recommended bool
}

// virtualNames mark an interface that is not the PC's own network.
var virtualNames = []string{"vethernet", "vmware", "virtualbox", "vbox", "docker", "br-", "veth", "wsl", "hyper-v", "loopback", "tailscale", "zerotier", "isatap", "teredo", "bluetooth"}

// dockerNames mark an interface that containers on this PC reach the host by.
var dockerNames = []string{"wsl", "docker", "vethernet"}

func hasAny(name string, words []string) bool {
	n := strings.ToLower(name)
	for _, w := range words {
		if strings.Contains(n, w) {
			return true
		}
	}
	return false
}

func usable4(a netip.Addr) bool {
	return a.Is4() && !a.IsLoopback() && !a.IsLinkLocalUnicast() && !a.IsUnspecified() && !a.IsMulticast()
}

// Candidates finds the addresses of this machine to offer. The first is the
// primary network address of an interface that is up and not virtual; the
// second is the host address containers and WSL on this PC reach (only when
// there is one); the last is the typed choice.
func Candidates(src Source, port string) ([]Candidate, error) {
	ifs, err := src.Interfaces()
	if err != nil {
		return nil, err
	}
	var pc, dock netip.Addr
	dockRank := 99
	for _, f := range ifs {
		if !f.Up || f.Loopback {
			continue
		}
		for _, a := range f.Addrs {
			if !usable4(a) {
				continue
			}
			switch {
			case hasAny(f.Name, dockerNames):
				rank := 1
				if strings.Contains(strings.ToLower(f.Name), "wsl") {
					rank = 0
				}
				if rank < dockRank {
					dock, dockRank = a, rank
				}
			case !hasAny(f.Name, virtualNames) && !pc.IsValid():
				pc = a
			}
		}
	}
	var out []Candidate
	if pc.IsValid() {
		out = append(out, Candidate{ID: ChoicePC, Label: "This PC's network address", Address: net.JoinHostPort(pc.String(), port),
			Who: "Runners and machines on your network, and containers on this PC.", Recommended: true})
	}
	if dock.IsValid() {
		out = append(out, Candidate{ID: ChoiceDocker, Label: "Docker and WSL on this PC only", Address: net.JoinHostPort(dock.String(), port),
			Who: "Containers and WSL on this PC. No other machine."})
	}
	out = append(out, Candidate{ID: ChoiceOther, Label: "Another address", Who: "Whoever can reach the address you type. It must be an address of this PC."})
	return out, nil
}

// Validate checks a typed host:port and returns it in its plain form. The host
// must be an address of this machine (or a wildcard), never loopback: the
// controller keeps 127.0.0.1 on its own.
func Validate(src Source, typed string) (string, error) {
	typed = strings.TrimSpace(typed)
	if typed == "" {
		return "", errors.New("write an address as host:port, such as 192.168.1.20:8460")
	}
	host, port, err := net.SplitHostPort(typed)
	if err != nil {
		// A bare address takes the default port.
		host, port = strings.Trim(typed, "[]"), DefaultPort
	}
	if n, perr := strconv.Atoi(port); perr != nil || n < 1 || n > 65535 {
		return "", fmt.Errorf("the port %q must be a number from 1 to 65535", port)
	}
	ip, perr := netip.ParseAddr(strings.Trim(host, "[]"))
	if perr != nil {
		return "", fmt.Errorf("%q is not an IP address; write the address of this PC, such as 192.168.1.20:8460", host)
	}
	ip = ip.Unmap()
	if ip.IsLoopback() {
		return "", errors.New("a loopback address only this PC can reach; the console keeps it already. Choose an address runners can reach")
	}
	if !ip.IsUnspecified() {
		ifs, err := src.Interfaces()
		if err != nil {
			return "", err
		}
		found := false
		for _, f := range ifs {
			for _, a := range f.Addrs {
				if a.Unmap() == ip {
					found = true
				}
			}
		}
		if !found {
			return "", fmt.Errorf("%s is not an address of this PC", ip)
		}
	}
	return net.JoinHostPort(ip.String(), port), nil
}

// Service is the stored listen address and what rules over it.
type Service struct {
	Settings *settings.Service
	Source   Source
	// Getenv reads the environment; nil uses none.
	Getenv func(string) string
	// Running is the address the controller bound at start, Fallback why it did
	// not bind the stored one (empty when it did).
	Running  string
	Fallback string
	// Agent is the agent link address, which the console address must not take.
	Agent string
}

// EnvName is the environment variable that wins over the stored choice.
const EnvName = "CRSP_LISTEN_ADDR"

func (s *Service) env() string {
	if s.Getenv == nil {
		return ""
	}
	return strings.TrimSpace(s.Getenv(EnvName))
}

// State is what the console shows.
type State struct {
	Running    string
	Stored     string
	FromEnv    bool
	EnvName    string
	Candidates []Candidate
	Problem    string
}

// State reads the stored choice and finds the candidates.
func (s *Service) State(ctx context.Context) (State, error) {
	st := State{Running: s.Running, FromEnv: s.env() != "", EnvName: EnvName, Problem: s.Fallback}
	v, err := s.Settings.GetListenAddress(ctx)
	if err != nil {
		return State{}, err
	}
	st.Stored = v.Address
	st.Candidates, err = Candidates(s.Source, DefaultPort)
	return st, err
}

// ErrFromEnv says the environment decides the address, so the console cannot.
var ErrFromEnv = errors.New("the address is set by the environment variable " + EnvName + " on this PC; change it there")

// ErrInvalid wraps the reason an address was refused.
type ErrInvalid struct{ Reason string }

func (e *ErrInvalid) Error() string { return e.Reason }

// Choose checks and stores the address, and writes the audit row.
func (s *Service) Choose(ctx context.Context, actor audit.Actor, typed string) (string, error) {
	if s.env() != "" {
		return "", ErrFromEnv
	}
	addr, err := Validate(s.Source, typed)
	if err != nil {
		return "", &ErrInvalid{Reason: err.Error()}
	}
	if s.Agent != "" {
		if _, ap, aerr := net.SplitHostPort(s.Agent); aerr == nil {
			if _, p, _ := net.SplitHostPort(addr); p == ap {
				return "", &ErrInvalid{Reason: "port " + p + " is the port of the agent link; choose another port"}
			}
		}
	}
	if _, err := s.Settings.SetListenAddress(ctx, actor, addr); err != nil {
		return "", err
	}
	return addr, nil
}

// Effective picks the address the controller binds: the environment, else the
// stored choice, else the configured one (file or default). fromSetting says
// the stored choice won.
func Effective(env, stored, configured string) (addr string, fromSetting bool) {
	switch {
	case strings.TrimSpace(env) != "":
		return configured, false
	case stored != "":
		return stored, true
	}
	return configured, false
}
