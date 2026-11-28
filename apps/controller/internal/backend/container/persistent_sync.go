package container

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/images"
)

// SupervisorSumFile is where a running pool supervisor keeps the checksum of
// the script it runs. A supervisor that writes it can load a newer copy of its
// script by itself; one that does not is older than R5.154 (runners R5.154).
const SupervisorSumFile = "/tmp/crsp-supervisor/supervisor.sum"

// syncRetryEvery is how often a restart that waits for an idle pool is tried
// again.
const syncRetryEvery = time.Minute

type persistentSync struct {
	mu   sync.Mutex
	done bool      // the scripts matched and the supervisor can reload
	next time.Time // the earliest time of the next try after a wait
}

// syncPersistentDue runs SyncPersistentFiles once per process, and again after
// a wait for an idle pool.
func (b *Backend) syncPersistentDue(ctx context.Context) error {
	s := &b.psync
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done || b.now().Before(s.next) {
		return nil
	}
	settled, err := b.SyncPersistentFiles(ctx)
	if err != nil {
		s.next = b.now().Add(syncRetryEvery)
		return err
	}
	if settled {
		s.done = true
	} else {
		s.next = b.now().Add(syncRetryEvery)
	}
	return nil
}

// SyncPersistentFiles brings the scripts in the pool container's /state/bin up
// to the ones this release ships, by checksum, and makes sure the running
// supervisor is the new one (runners R5.154):
//   - a script that differs or is missing is rewritten;
//   - a supervisor that writes its checksum loads the new copy by itself, between
//     two ticks, while no runner has a job;
//   - an older supervisor cannot, so the container is restarted, but only while
//     no runner of it has a job; otherwise it waits and the next call tries again.
//
// A pool that is not there or not running has nothing to bring up to date: the
// next start writes the files. settled is false while a restart waits.
func (b *Backend) SyncPersistentFiles(ctx context.Context) (settled bool, err error) {
	exists, running, _, err := b.poolState(ctx, PersistentPool)
	if err != nil {
		return false, err
	}
	if !exists || !running {
		return true, nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	have, err := b.persistentSums(ctx)
	if err != nil {
		return false, err
	}
	var wrote []string
	for _, n := range persistentFiles {
		body, err := images.FS.ReadFile("persistent/" + n)
		if err != nil {
			return false, err
		}
		sum := sha256.Sum256(body)
		if have[n] == hex.EncodeToString(sum[:]) {
			continue
		}
		if err := b.writePersistentFile(ctx, n, body); err != nil {
			return false, err
		}
		wrote = append(wrote, n)
	}
	if len(wrote) > 0 {
		b.log("sync", "persistent scripts brought up to date: "+strings.Join(wrote, ", "))
	}
	res, err := b.run(ctx, "exec", PersistentPool, "test", "-f", SupervisorSumFile)
	if err != nil {
		return false, err
	}
	if res.ExitCode == 0 {
		return true, nil // a supervisor that reloads by itself
	}
	busy, err := b.persistentBusy(ctx)
	if err != nil {
		return false, err
	}
	if busy {
		b.log("sync", "the pool supervisor is older than this release; it is restarted when no runner has a job")
		return false, nil
	}
	if err := b.step(ctx, "pool restart", []string{"restart", PersistentPool}, ""); err != nil {
		return false, err
	}
	b.log("sync", "the pool container was restarted to load the new supervisor")
	return true, nil
}

// persistentSums reads the sha256 of every script in the pool's bin folder.
func (b *Backend) persistentSums(ctx context.Context) (map[string]string, error) {
	res, err := b.run(ctx, "exec", PersistentPool, "sh", "-c", `cd "$1/bin" 2>/dev/null && sha256sum -- *.sh 2>/dev/null; true`, "sh", StateDir)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("persistent sums: %s", b.redact(strings.TrimSpace(res.Stderr)))
	}
	out := map[string]string{}
	for _, l := range strings.Split(res.Stdout, "\n") {
		f := strings.Fields(l)
		if len(f) == 2 {
			out[strings.TrimPrefix(f[1], "*")] = f[0]
		}
	}
	return out, nil
}

// persistentBusy is true while some runner of the container has a job (the
// job-started hook leaves a file in $STATE/busy).
func (b *Backend) persistentBusy(ctx context.Context) (bool, error) {
	res, err := b.run(ctx, "exec", PersistentPool, "sh", "-c", `ls -A "$1/busy" 2>/dev/null; true`, "sh", StateDir)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(res.Stdout) != "", nil
}
