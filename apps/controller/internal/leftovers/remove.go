package leftovers

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"
)

// needsAdmin is the error of an item that needs rights the agent lacks.
const needsAdmin = "the agent needs administrator rights to remove this"

const notALeftover = "This is not a leftover on this machine: no leftover scan finds it now."

func isGone(err error) bool {
	return errors.Is(err, ErrNotExist) || errors.Is(err, fs.ErrNotExist)
}

// verdict is the answer of the checks before an item is touched.
type verdict int

const (
	proceed verdict = iota
	refuse
	gone
)

// check decides one ref against a fresh snapshot. A refusal carries its words.
func (sn *snapshot) check(o OS, ref Ref, found map[string]Item) (verdict, string) {
	switch ref.Kind {
	case KindService:
		if !strings.HasPrefix(ref.Name, ServicePrefix) {
			return refuse, "This is not a runner service: its name does not start with " + ServicePrefix + "."
		}
		var svc *OSService
		for i := range sn.svcs {
			if sn.st.key(sn.svcs[i].Name) == sn.st.key(ref.Name) {
				svc = &sn.svcs[i]
			}
		}
		if svc == nil {
			return gone, ""
		}
		if sn.serviceOwned(*svc) {
			return refuse, "This service belongs to a runner of this controller."
		}
	case KindAccount:
		if !crspName(ref.Name) {
			return refuse, "This is not a crsp runner account: its name does not start with crsp-."
		}
		var acc *OSAccount
		for i := range sn.accts {
			if sn.st.key(sn.accts[i].Name) == sn.st.key(ref.Name) {
				acc = &sn.accts[i]
			}
		}
		if acc == nil {
			return gone, ""
		}
		if strings.TrimSpace(acc.Description) != AccountDescription {
			return refuse, "This is not a crsp runner account: its description is not " + AccountDescription + "."
		}
		if sn.ownedAccounts[sn.st.key(acc.Name)] {
			return refuse, "This account belongs to a runner of this controller."
		}
	case KindFolder, KindDownload:
		if msg := sn.unsafePath(o, ref.Name); msg != "" {
			return refuse, msg
		}
		if ref.Kind == KindFolder {
			exists := false
			for _, d := range sn.dirs {
				if sn.st.same(d.Path, ref.Name) {
					exists = true
					if sn.ownedNames[sn.st.key(d.Name)] {
						return refuse, "This folder belongs to a runner of this controller."
					}
				}
			}
			if !exists {
				return gone, ""
			}
		} else {
			exists := false
			for _, f := range sn.files {
				if sn.st.same(f.Path, ref.Name) {
					exists = true
					if sn.ownedNames[sn.st.key(downloadRunner(f.Name))] {
						return refuse, "This download belongs to a runner of this controller."
					}
				}
			}
			if !exists {
				return gone, ""
			}
		}
	default:
		return refuse, fmt.Sprintf("Unknown kind %q.", string(ref.Kind))
	}
	if _, ok := found[canonID(sn.st, ref)]; !ok {
		return refuse, notALeftover
	}
	return proceed, ""
}

func canonID(st style, r Ref) string {
	if r.Kind == KindFolder || r.Kind == KindDownload {
		if segs, ok := st.split(r.Name); ok {
			return string(r.Kind) + ":" + st.key(st.join(segs))
		}
	}
	return string(r.Kind) + ":" + st.key(r.Name)
}

// unsafePath is "" when p lies strictly inside the runners folder, has no
// `..` and goes through no link; else the reason in words.
func (sn *snapshot) unsafePath(o OS, p string) string {
	rel, ok := sn.st.under(sn.root, p)
	if !ok {
		return "This path is not inside the crsp runners folder " + sn.root + "."
	}
	rootReal, err := o.Resolve(sn.root)
	if err != nil {
		rootReal = sn.root
	}
	real, err := o.Resolve(p)
	if err != nil {
		return "" // not there (or unreadable): the existence check decides
	}
	rs, _ := sn.st.split(rootReal)
	if !sn.st.same(real, sn.st.join(append(rs, rel...))) {
		return "This path goes through a link out of the crsp runners folder."
	}
	return ""
}

// Remove removes the refs. Each ref is checked against a fresh scan first: it
// is acted on only when the scan still finds it and no runner of owned owns it.
// Services go first, then folders, accounts and downloads. An item that fails
// does not stop the others; every item has its own Result, in ref order.
func Remove(ctx context.Context, o OS, owned []Owned, refs []Ref, log func(string)) ([]Result, error) {
	return RemoveAt(ctx, o, owned, refs, time.Now(), log)
}

// RemoveAt is Remove with the clock of the fresh scan given (tests).
func RemoveAt(ctx context.Context, o OS, owned []Owned, refs []Ref, now time.Time, log func(string)) ([]Result, error) {
	if log == nil {
		log = func(string) {}
	}
	sn, err := take(ctx, o, owned)
	if err != nil {
		return nil, err
	}
	found := map[string]Item{}
	for _, it := range sn.items(ctx, o, now) {
		found[canonID(sn.st, Ref{Kind: it.Kind, Name: it.Name})] = it
	}
	results := make([]Result, len(refs))
	todo := make([]bool, len(refs))
	for i, ref := range refs {
		res := Result{Kind: ref.Kind, Name: ref.Name}
		switch v, why := sn.check(o, ref, found); v {
		case refuse:
			res.Outcome, res.Error = Refused, why
			log(fmt.Sprintf("refused %s %s: %s", ref.Kind, ref.Name, why))
		case gone:
			res.Outcome = AlreadyGone
			log(fmt.Sprintf("%s %s is already gone", ref.Kind, ref.Name))
		default:
			todo[i] = true
		}
		results[i] = res
	}
	admin := o.Admin()
	for _, kind := range []Kind{KindService, KindFolder, KindAccount, KindDownload} {
		for i, ref := range refs {
			if !todo[i] || ref.Kind != kind {
				continue
			}
			res := &results[i]
			step := func(s string) {
				res.Steps = append(res.Steps, s)
				log(s)
			}
			fail := func(msg string) {
				res.Outcome, res.Error = Failed, msg
				log(fmt.Sprintf("failed %s %s: %s", ref.Kind, ref.Name, msg))
			}
			if (kind == KindService || kind == KindAccount) && !admin {
				fail(needsAdmin)
				continue
			}
			switch kind {
			case KindService:
				removeService(ctx, o, ref.Name, res, log, step, fail)
			case KindFolder, KindDownload:
				removePath(ctx, o, sn, ref, res, step, fail)
			case KindAccount:
				switch err := o.DeleteAccount(ctx, ref.Name); {
				case err == nil:
					step("deleted account " + ref.Name)
					res.Outcome = Removed
				case isGone(err):
					res.Outcome = AlreadyGone
					log("account " + ref.Name + " is already gone")
				default:
					fail("could not delete the account: " + err.Error())
				}
			}
		}
	}
	return results, nil
}

func removeService(ctx context.Context, o OS, name string, res *Result, log, step, fail func(string)) {
	switch err := o.StopService(ctx, name); {
	case err == nil:
		step("stopped service " + name)
	case isGone(err):
		res.Outcome = AlreadyGone
		log("service " + name + " is already gone")
		return
	default:
		// A service that cannot be stopped may still be deleted; Windows
		// then removes it when it ends.
		step("could not stop service " + name + ": " + err.Error())
	}
	switch err := o.DeleteService(ctx, name); {
	case err == nil:
		step("deleted service " + name)
		res.Outcome = Removed
	case isGone(err):
		res.Outcome = AlreadyGone
	default:
		fail("could not delete the service: " + err.Error())
	}
}

func removePath(ctx context.Context, o OS, sn *snapshot, ref Ref, res *Result, step func(string), fail func(string)) {
	var size int64
	what := "folder"
	for _, d := range sn.dirs {
		if ref.Kind == KindFolder && sn.st.same(d.Path, ref.Name) {
			size, _ = o.DirSize(ctx, d.Path)
		}
	}
	if ref.Kind == KindDownload {
		what = "download"
		for _, f := range sn.files {
			if sn.st.same(f.Path, ref.Name) {
				size = f.Size
			}
		}
	}
	switch err := o.RemoveAll(ref.Name); {
	case err == nil:
		step(fmt.Sprintf("removed %s %s (%d bytes)", what, ref.Name, size))
		res.Outcome = Removed
	case isGone(err):
		res.Outcome = AlreadyGone
	default:
		fail("could not remove the " + what + ": " + err.Error())
	}
}
