//go:build !windows

package tray

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSecondInstanceExits0(t *testing.T) {
	p := filepath.Join(t.TempDir(), "l.lock")
	l1, ok, err := acquireLockAt(p)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if _, ok, _ := acquireLockAt(p); ok {
		t.Fatal("second instance got the lock; it must exit 0 at once")
	}
	l1.Release()
	l3, ok, _ := acquireLockAt(p)
	if !ok {
		t.Fatal("lock not released")
	}
	l3.Release()
}

func TestSocketClientOverUnix(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "t.sock")
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	if err := os.Chmod(sock, 0o660); err != nil {
		t.Fatal(err)
	}
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		line, _ := bufio.NewReader(c).ReadString('\n')
		var r Request
		_ = json.Unmarshal([]byte(line), &r)
		_, _ = c.Write([]byte(`{"ok":true,"state":"ok","running":2,"waiting":1,"runners":1}` + "\n"))
	}()
	t.Setenv("CRSP_TRAY_SOCKET", sock)
	st, err := NewClient().Status(context.Background())
	if err != nil || st.Running != 2 || !strings.Contains(st.Text(), "2 running") {
		t.Fatalf("%+v %v", st, err)
	}
	_ = time.Second
}
