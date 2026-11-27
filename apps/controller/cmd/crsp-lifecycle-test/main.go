// Command crsp-lifecycle-test is the test interface of the lifecycle standard. The names match
// docs/design-system/next-version/lifecycle.json test_interface:
//
//	crsp-lifecycle-test --self-test           poll the installed console until it answers (LC-8)
//	crsp-lifecycle-test --lifecycle-state     print the state of a fresh, unpaired updater as JSON
//	crsp-lifecycle-test --lifecycle-simulate  run the pair step on the updater: no manual step (LC-29)
//	crsp-lifecycle-test --db-snapshot DB OUT  write the user tables of a database file, read-only, as JSON (LC-10, LC-11)
//	crsp-lifecycle-test --db-compare BEFORE AFTER  fail when a snapshot lost or changed a row of the earlier one
//	crsp-lifecycle-test [--addr 127.0.0.1:7391]  serve the updater test interface over HTTP for
//	                                          scripts/delivery/lifecycle_conformance.py --url
//
// It is a test tool: the release workflow does not ship it.
package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/lifecycletest"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/listenaddr"
)

const (
	selfTestLimit = 180 * time.Second // contract default: self-test limit 180 s
	selfTestPoll  = 2 * time.Second   // contract default: poll every 2 s
)

// consoleHealthURL is the installed console's health address on loopback: the
// port of CRSP_LISTEN_ADDR when set, else the console's default port.
func consoleHealthURL() string {
	port := listenaddr.DefaultPort
	if _, p, err := net.SplitHostPort(os.Getenv(listenaddr.EnvName)); err == nil && p != "" {
		port = p
	}
	return "http://" + net.JoinHostPort("127.0.0.1", port) + "/healthz"
}

func main() {
	selfT := flag.Bool("self-test", false, "poll the installed console until it answers (LC-8)")
	state := flag.Bool("lifecycle-state", false, "print the state of a fresh, unpaired updater")
	sim := flag.Bool("lifecycle-simulate", false, "pair the updater and check it left unpaired (LC-29)")
	addr := flag.String("addr", "127.0.0.1:7391", "listen address (loopback only)")
	snap := flag.Bool("db-snapshot", false, "write the user tables of a database (args: DB OUT)")
	cmp := flag.Bool("db-compare", false, "compare two snapshots (args: BEFORE AFTER)")
	flag.Parse()
	var err error
	switch {
	case *snap || *cmp:
		if flag.NArg() != 2 {
			err = errors.New("two file arguments are needed")
		} else if *snap {
			err = dbSnapshot(flag.Arg(0), flag.Arg(1))
		} else {
			err = dbCompare(flag.Arg(0), flag.Arg(1))
		}
	case *selfT:
		err = selfTest(consoleHealthURL(), selfTestLimit, selfTestPoll)
	case *state:
		err = printState()
	case *sim:
		err = pair()
	default:
		srv := &http.Server{Addr: *addr, Handler: lifecycletest.Handler(), ReadHeaderTimeout: 5 * time.Second}
		log.Printf("crsp-lifecycle-test on %s", *addr)
		log.Fatal(srv.ListenAndServe())
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

// printState resets the updater to an unpaired machine and prints its state.
func printState() error {
	srv := httptest.NewServer(lifecycletest.Handler())
	defer srv.Close()
	if err := reset(srv.URL); err != nil {
		return err
	}
	st, err := call(srv.URL, "state", nil)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(st)
}

func reset(base string) error {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	_, err = call(base, "reset", map[string]interface{}{"version": "1.0.0", "paired": false, "policy": "automatic_when_idle",
		"channel": "stable", "idle": true, "public_key": hex.EncodeToString(pub), "seed": 1})
	return err
}

// selfTest polls url until it answers 200 or the limit passes.
func selfTest(url string, limit, poll time.Duration) error {
	deadline := time.Now().Add(limit)
	c := http.Client{Timeout: 2 * time.Second}
	for {
		r, err := c.Get(url) //nolint:noctx // short probe of the local console
		if err == nil {
			_ = r.Body.Close()
			if r.StatusCode == http.StatusOK {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("no answer from %s within %s", url, limit)
		}
		time.Sleep(poll)
	}
}

// pair resets the updater to an unpaired machine, pairs it and checks that it left "unpaired".
func pair() error {
	srv := httptest.NewServer(lifecycletest.Handler())
	defer srv.Close()
	if err := reset(srv.URL); err != nil {
		return err
	}
	before, err := call(srv.URL, "state", nil)
	if err != nil || before["state"] != "unpaired" {
		return fmt.Errorf("an unpaired machine must say so, got %v (%v)", before["state"], err)
	}
	after, err := call(srv.URL, "pair", map[string]interface{}{"paired": true})
	if err != nil {
		return err
	}
	if after["paired"] != true || after["state"] == "unpaired" {
		return fmt.Errorf("still %v after pairing", after["state"])
	}
	return nil
}

func call(base, verb string, body map[string]interface{}) (map[string]interface{}, error) {
	b, _ := json.Marshal(body)
	r, err := http.Post(base+"/test/"+verb, "application/json", bytes.NewReader(b)) //nolint:noctx,gosec // in-process test server
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Body.Close() }()
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	var out map[string]interface{}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	if e, ok := out["error"].(string); ok {
		return nil, errors.New(e)
	}
	return out, nil
}
