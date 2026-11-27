package container

import (
	"errors"
	"strings"
	"testing"
)

func TestCrspMountedReadOnlyNoSocket(t *testing.T) {
	args, err := CrspMountArgs("/opt/crsp-host/crsp-linux")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(args, " ")
	for _, want := range []string{"type=bind", "source=/opt/crsp-host,target=/opt/crsp-bin,readonly", "CRSP_BIN=/opt/crsp-bin/crsp-linux"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q lacks %q", got, want)
		}
	}
	if strings.Contains(got, "source=/opt/crsp-host/crsp-linux") {
		t.Errorf("the file itself is mounted: %q", got)
	}
	wa, err := CrspMountArgs("C:\\crsp\\crsp-linux")
	if err != nil {
		t.Errorf("windows path: %v", err)
	}
	if g := strings.Join(wa, " "); !strings.Contains(g, "source=C:\\crsp,target=/opt/crsp-bin,readonly") || !strings.Contains(g, "CRSP_BIN=/opt/crsp-bin/crsp-linux") {
		t.Errorf("windows mount %q", g)
	}
	for _, bad := range []string{"/var/run/docker.sock", "/run/podman/podman.sock", "\\\\.\\pipe\\docker_engine", ""} {
		if _, err := CrspMountArgs(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if _, err := CrspMountArgs("/run/docker.sock"); !errors.Is(err, ErrSocketMount) {
		t.Errorf("err %v", err)
	}
	if _, err := CrspMountArgs("/a,b"); err == nil {
		t.Error("comma accepted")
	}
}
