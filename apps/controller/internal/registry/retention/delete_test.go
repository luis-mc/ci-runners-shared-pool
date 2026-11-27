package retention

import (
	"context"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
)

// R5.46: delete without confirm answers needs-confirmation with what uses the image.
func TestDeleteNeedsConfirmationAndNamesWhatUsesIt(t *testing.T) {
	e := newEnv(t)
	e.repo.add(registry.Image{ID: "i1", State: registry.StateSuperseded, KeepUntil: day(3)})
	e.repo.pointers["i1"] = []registry.Pointer{{Repository: "o/app", Profile: "node", Arch: "amd64", ImageID: "i1"}}
	e.repo.running["i1"] = []string{"o/app ci.yml test (started 14:05 UTC)"}
	u, err := e.svc.Delete(context.Background(), "i1", false, "luis")
	if !isErr(err, ErrNeedsConfirmation) {
		t.Fatalf("got %v", err)
	}
	if len(u.RunningJobs) != 1 || u.RunningJobs[0] != "o/app ci.yml test (started 14:05 UTC)" || len(u.Pointers) != 1 || !u.InRollback {
		t.Fatalf("InUse does not name what uses it: %+v", u)
	}
	if len(e.reg.deleted) != 0 || e.repo.images["i1"].State == registry.StateDeleted {
		t.Fatal("deleted without a yes")
	}
	// Even an image nothing uses needs the yes.
	e.repo.add(registry.Image{ID: "free", State: registry.StateCurrent})
	if _, err := e.svc.Delete(context.Background(), "free", false, "luis"); !isErr(err, ErrNeedsConfirmation) {
		t.Fatalf("got %v", err)
	}
}

// R5.46: an image a running job uses is refused even with confirm, and the job is named.
func TestDeleteRefusedWhileAJobUsesIt(t *testing.T) {
	e := newEnv(t)
	e.repo.add(registry.Image{ID: "i1", State: registry.StateCurrent})
	e.repo.running["i1"] = []string{"o/app ci.yml test"}
	u, err := e.svc.Delete(context.Background(), "i1", true, "luis")
	if !isErr(err, registry.ErrInUse) {
		t.Fatalf("got %v", err)
	}
	if len(u.RunningJobs) != 1 || u.RunningJobs[0] != "o/app ci.yml test" {
		t.Fatalf("the job is not named: %+v", u)
	}
	if len(e.reg.deleted) != 0 || len(e.aud.entries) != 0 {
		t.Fatal("changed something on a refusal")
	}
}

// R5.47: a token that has not ended and names the image holds the delete back.
func TestDeleteRefusedWhileATokenNamesIt(t *testing.T) {
	e := newEnv(t)
	e.repo.add(registry.Image{ID: "i1", Name: "crsp/std/node", State: registry.StateCurrent})
	e.repo.leases["crsp/std/node"] = 1
	if _, err := e.svc.Delete(context.Background(), "i1", true, "luis"); !isErr(err, registry.ErrInUse) {
		t.Fatalf("got %v", err)
	}
}

// R5.46, R5.51: with a yes, the manifest goes, blobs wait for the cleanup, and the delete is audited.
func TestDeleteWithConfirmRemovesManifestOnly(t *testing.T) {
	e := newEnv(t)
	e.repo.add(registry.Image{ID: "i1", Name: "crsp/std/node", Manifest: "sha256:abc", State: registry.StateCurrent})
	e.repo.layers = nil
	must(t, e.svc.Evaluate(context.Background(), t0)) // seeds nothing
	_, err := e.svc.Delete(context.Background(), "i1", true, "luis")
	must(t, err)
	if len(e.reg.deleted) != 1 || e.reg.deleted[0] != "crsp/std/node@sha256:abc" {
		t.Fatalf("manifest delete: %v", e.reg.deleted)
	}
	if len(e.reg.gcDry) != 0 {
		t.Fatal("a delete ran the garbage collection")
	}
	if e.repo.images["i1"].State != registry.StateDeleted {
		t.Fatal("not marked deleted")
	}
	if len(e.aud.entries) != 1 || e.aud.entries[0].Action != ActionDelete || e.aud.entries[0].Actor.Login != "luis" || e.aud.entries[0].ID != "i1" {
		t.Fatalf("audit: %+v", e.aud.entries)
	}
	// Deleting again: not found.
	if _, err := e.svc.Delete(context.Background(), "i1", true, "luis"); !isErr(err, registry.ErrNotFound) {
		t.Fatalf("got %v", err)
	}
	if _, err := e.svc.Delete(context.Background(), "nope", true, "luis"); !isErr(err, registry.ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}

// R5.51: the audit log fails closed: no entry, the delete is reported as failed.
func TestDeleteFailsWhenAuditFails(t *testing.T) {
	e := newEnv(t)
	e.repo.add(registry.Image{ID: "i1", State: registry.StateCurrent})
	e.aud.err = context.DeadlineExceeded
	if _, err := e.svc.Delete(context.Background(), "i1", true, "luis"); err == nil {
		t.Fatal("audit failure ignored")
	}
}

// R5.46: an image with a pointer may go with the owner's yes (the card named it); the open recommendations on it close.
func TestDeletePointedImageWithConfirmClosesRecommendations(t *testing.T) {
	e := newEnv(t)
	e.repo.add(registry.Image{ID: "i1", State: registry.StateCurrent, PromotedAt: day(-90), LastPullAt: day(-45)})
	must(t, e.svc.Evaluate(context.Background(), t0))
	if _, ok := e.recFor(registry.RuleUnused, "i1"); !ok {
		t.Fatal("setup: no unused recommendation")
	}
	e.repo.pointers["i1"] = []registry.Pointer{{Repository: "o/app", ImageID: "i1"}}
	if u, err := e.svc.Delete(context.Background(), "i1", true, "luis"); err != nil || len(u.Pointers) != 1 {
		t.Fatalf("%v %+v", err, u)
	}
	if _, ok := e.recFor(registry.RuleUnused, "i1"); ok {
		t.Fatal("recommendation stays after the image was deleted")
	}
}

func TestCheckDeleteEmptyWhenNothingUsesTheImage(t *testing.T) {
	e := newEnv(t)
	e.repo.add(registry.Image{ID: "i1", State: registry.StateCurrent})
	u, err := e.svc.CheckDelete(context.Background(), "i1")
	must(t, err)
	if !u.Empty() {
		t.Fatalf("%+v", u)
	}
}
