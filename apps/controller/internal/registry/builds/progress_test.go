package builds

import (
	"strings"
	"testing"
)

const plainLog = `#1 [internal] load build definition from Dockerfile
#1 DONE 0.0s
#2 [stage-1 1/4] FROM docker.io/library/ubuntu:24.04@sha256:abcd
#2 sha256:0123456789abcdef 12.3MB / 45.6MB 1.2s
#2 sha256:0123456789abcdef 45.6MB / 45.6MB 3.0s
#2 DONE 4.1s
#3 [stage-1 2/4] RUN apt-get update
#3 0.411 Get:1 http://archive.ubuntu.com
#3 CACHED
#4 [stage-1 3/4] COPY crsp /usr/local/bin/crsp
#4 DONE 0.2s
#5 [stage-1 4/4] RUN install-layers
#5 ERROR: process "/bin/sh -c install-layers" did not complete successfully: exit code: 100
`

func TestParserFollowsSteps(t *testing.T) {
	p := NewParser()
	var seen []Progress
	for _, l := range strings.Split(plainLog, "\n") {
		if pr, ok := p.Feed(l); ok {
			seen = append(seen, pr)
		}
	}
	first := seen[0]
	if first.Step != 1 || first.Total != 4 || first.State != StepRunning || !strings.HasPrefix(first.Name, "FROM docker.io") {
		t.Fatalf("first step: %+v", first)
	}
	blob := seen[1]
	if blob.Layer != "0123456789ab" || blob.BytesDone != 12_300_000 || blob.BytesTotal != 45_600_000 {
		t.Fatalf("blob: %+v", blob)
	}
	var done Progress
	for _, s := range seen {
		if s.Step == 1 && s.State == StepDone {
			done = s
		}
	}
	if done.StepSeconds != 4.1 {
		t.Fatalf("step 1 done: %+v", done)
	}
	var cached bool
	for _, s := range seen {
		if s.Step == 2 && s.State == StepCached {
			cached = true
		}
	}
	if !cached {
		t.Fatal("step 2 should be cached")
	}
	last := p.Current()
	if last.Step != 4 || last.Total != 4 || last.State != StepFailed || last.Name != "RUN install-layers" {
		t.Fatalf("failure: %+v", last)
	}
}

func TestParserIgnoresInternalStepsAndNoise(t *testing.T) {
	p := NewParser()
	for _, l := range []string{"#1 [internal] load .dockerignore", "#1 DONE 0.0s", "#1 transferring context: 2B done", "", "hello"} {
		if _, ok := p.Feed(l); ok {
			t.Fatalf("%q should not change the progress", l)
		}
	}
	if p.Current().Total != 0 {
		t.Fatalf("no step seen yet: %+v", p.Current())
	}
}

func TestParserClassicBuilder(t *testing.T) {
	p := NewParser()
	pr, ok := p.Feed("Step 3/9 : RUN apt-get update")
	if !ok || pr.Step != 3 || pr.Total != 9 || pr.Name != "RUN apt-get update" {
		t.Fatalf("classic: %+v %v", pr, ok)
	}
}

func TestParserSecondStageDoesNotGoBack(t *testing.T) {
	p := NewParser()
	p.Feed("#2 [build 5/5] RUN make")
	pr, _ := p.Feed("#3 [stage-1 1/5] FROM base")
	if pr.Step != 5 {
		t.Fatalf("a lower step of the same total must not move the build back: %+v", pr)
	}
}

func TestParseBytesUnits(t *testing.T) {
	for _, c := range []struct {
		n, u string
		want int64
	}{{"1.5", "kB", 1500}, {"2", "MB", 2_000_000}, {"1", "GB", 1_000_000_000}, {"3", "B", 3}, {"1", "MiB", 1 << 20}} {
		if got := parseBytes(c.n, c.u); got != c.want {
			t.Errorf("%s%s: %d, want %d", c.n, c.u, got, c.want)
		}
	}
}
