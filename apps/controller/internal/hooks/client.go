package hooks

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler/slots"
)

// Hello is what the agent tells a hook about its machine.
type Hello struct {
	MachineID string         `json:"machine_id"`
	Settings  slots.Settings `json:"settings"`
}

// Client is the agent link as a hook sees it. The scheduler Engine satisfies
// the queue part, so tests and an in-process agent can use it directly.
type Client interface {
	Hello(ctx context.Context) (Hello, error)
	Gate(ctx context.Context, req scheduler.GateRequest) (scheduler.Decision, error)
	Beat(ctx context.Context, machineID, gateID string, load scheduler.Load) (scheduler.Decision, error)
	Cancel(ctx context.Context, machineID, gateID string) error
	Release(ctx context.Context, machineID string, job scheduler.JobRef, claims []string) ([]scheduler.Freed, error)
}

// Time limits of the hook's one network-like path, the call to the agent
// (scheduling R5.62). A hook does only local work; this call is the only thing
// that can be slow, so it has a short, explicit limit and the hook decides what
// the job does when it is hit: it fails open with a ::warning annotation and a
// problem record, and never waits for a login or a reply (R5.61).
const (
	// CallTimeout is how long a hook waits for one answer of the agent.
	CallTimeout = 5 * time.Second
	// AgentForwardTimeout is how long the agent waits for the controller while
	// it answers a hook. It is shorter than CallTimeout, so the hook gets the
	// agent's "not reachable" answer rather than its own timeout.
	AgentForwardTimeout = 4 * time.Second
)

// ErrUnreachable marks a failure to reach the agent, or the agent's failure to
// reach the controller. The gate then waits on local load only (R5.23).
var ErrUnreachable = errors.New("agent not reachable")

// envelope is one JSON line of the hook and agent protocol.
type envelope struct {
	Op        string                 `json:"op"`
	MachineID string                 `json:"machine_id,omitempty"`
	GateID    string                 `json:"gate_id,omitempty"`
	Gate      *scheduler.GateRequest `json:"gate,omitempty"`
	Load      *scheduler.Load        `json:"load,omitempty"`
	Job       *scheduler.JobRef      `json:"job,omitempty"`
	Claims    []string               `json:"claims,omitempty"`
}

type reply struct {
	Error    string              `json:"error,omitempty"`
	Gone     bool                `json:"gone,omitempty"` // the ticket is gone (ErrTicketGone)
	Hello    *Hello              `json:"hello,omitempty"`
	Decision *scheduler.Decision `json:"decision,omitempty"`
	Freed    []scheduler.Freed   `json:"freed,omitempty"`
}

// SocketClient talks to the agent on a Unix domain socket, one request and one
// answer per connection.
type SocketClient struct {
	Path    string
	Timeout time.Duration
}

var _ Client = (*SocketClient)(nil)

func (c *SocketClient) call(ctx context.Context, env envelope) (reply, error) {
	to := c.Timeout
	if to <= 0 {
		to = CallTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, to)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", c.Path)
	if err != nil {
		return reply{}, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	defer func() { _ = conn.Close() }()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	if err := json.NewEncoder(conn).Encode(env); err != nil {
		return reply{}, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	var r reply
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&r); err != nil {
		return reply{}, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	if r.Error != "" {
		return r, fmt.Errorf("%w: %s", ErrUnreachable, r.Error)
	}
	return r, nil
}

// Hello implements Client.
func (c *SocketClient) Hello(ctx context.Context) (Hello, error) {
	r, err := c.call(ctx, envelope{Op: "hello"})
	if err != nil || r.Hello == nil {
		return Hello{}, orUnreachable(err)
	}
	return *r.Hello, nil
}

// Gate implements Client.
func (c *SocketClient) Gate(ctx context.Context, req scheduler.GateRequest) (scheduler.Decision, error) {
	r, err := c.call(ctx, envelope{Op: "gate", Gate: &req})
	return decision(r, err)
}

// Beat implements Client.
func (c *SocketClient) Beat(ctx context.Context, machineID, gateID string, load scheduler.Load) (scheduler.Decision, error) {
	r, err := c.call(ctx, envelope{Op: "beat", MachineID: machineID, GateID: gateID, Load: &load})
	if r.Gone {
		return scheduler.Decision{}, scheduler.ErrTicketGone
	}
	return decision(r, err)
}

// Cancel implements Client.
func (c *SocketClient) Cancel(ctx context.Context, machineID, gateID string) error {
	_, err := c.call(ctx, envelope{Op: "cancel", MachineID: machineID, GateID: gateID})
	return err
}

// Release implements Client.
func (c *SocketClient) Release(ctx context.Context, machineID string, job scheduler.JobRef, claims []string) ([]scheduler.Freed, error) {
	r, err := c.call(ctx, envelope{Op: "release", MachineID: machineID, Job: &job, Claims: claims})
	return r.Freed, err
}

func decision(r reply, err error) (scheduler.Decision, error) {
	if err != nil {
		return scheduler.Decision{}, err
	}
	if r.Decision == nil {
		return scheduler.Decision{}, ErrUnreachable
	}
	return *r.Decision, nil
}

func orUnreachable(err error) error {
	if err == nil {
		return ErrUnreachable
	}
	return err
}

// Serve answers hook requests on ln with h until ln is closed. It is the
// agent's side of the protocol: the agent passes a Client that forwards to the
// controller over its stream. Each connection is one request.
func Serve(ln net.Listener, h Client) error {
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { _ = conn.Close() }()
			_ = conn.SetDeadline(time.Now().Add(time.Minute))
			var env envelope
			if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&env); err != nil {
				return
			}
			_ = json.NewEncoder(conn).Encode(handle(context.Background(), h, env))
		}()
	}
}

func handle(ctx context.Context, h Client, env envelope) reply {
	// A slow controller never holds a hook past its own limit: the agent answers
	// "not reachable" first and the hook fails open (R5.62).
	ctx, cancel := context.WithTimeout(ctx, AgentForwardTimeout)
	defer cancel()
	var r reply
	var err error
	switch env.Op {
	case "hello":
		var he Hello
		if he, err = h.Hello(ctx); err == nil {
			r.Hello = &he
		}
	case "gate":
		if env.Gate == nil {
			return reply{Error: "gate request is missing"}
		}
		var d scheduler.Decision
		if d, err = h.Gate(ctx, *env.Gate); err == nil {
			r.Decision = &d
		}
	case "beat":
		var load scheduler.Load
		if env.Load != nil {
			load = *env.Load
		}
		var d scheduler.Decision
		d, err = h.Beat(ctx, env.MachineID, env.GateID, load)
		if errors.Is(err, scheduler.ErrTicketGone) {
			return reply{Gone: true}
		}
		if err == nil {
			r.Decision = &d
		}
	case "cancel":
		err = h.Cancel(ctx, env.MachineID, env.GateID)
	case "release":
		if env.Job == nil {
			return reply{Error: "job is missing"}
		}
		r.Freed, err = h.Release(ctx, env.MachineID, *env.Job, env.Claims)
	default:
		return reply{Error: "unknown op " + strings.TrimSpace(env.Op)}
	}
	if err != nil {
		return reply{Error: err.Error()}
	}
	return r
}
