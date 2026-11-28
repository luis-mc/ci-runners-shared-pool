package main

import (
	"context"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/poolname"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/repos"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
)

// renameRunners gives the pool rename the runners service (runners R5.141 to R5.145).
type renameRunners struct{ svc *runners.Service }

func (p renameRunners) WithLabel(ctx context.Context, label string) ([]poolname.RunnerRef, error) {
	hs, err := p.svc.LabelHolders(ctx, label)
	if err != nil {
		return nil, err
	}
	out := make([]poolname.RunnerRef, 0, len(hs))
	for _, h := range hs {
		out = append(out, poolname.RunnerRef{ID: h.ID, Name: h.Name, Registered: h.Registered, Ephemeral: h.Ephemeral})
	}
	return out, nil
}

func (p renameRunners) AddLabel(ctx context.Context, id, label, actor string) error {
	return p.svc.AddPoolLabel(ctx, id, label, actor)
}

func (p renameRunners) RemoveLabel(ctx context.Context, id, label, actor string) error {
	return p.svc.RemovePoolLabel(ctx, id, label, actor)
}

// renameRepos gives the pool rename the repositories service.
type renameRepos struct{ svc *repos.Service }

func (p renameRepos) PoolOn(ctx context.Context) ([]poolname.RepoRef, error) {
	rs, err := p.svc.PoolOnRepositories(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]poolname.RepoRef, 0, len(rs))
	for _, r := range rs {
		out = append(out, poolname.RepoRef{ID: r.ID, FullName: r.FullName})
	}
	return out, nil
}

func (p renameRepos) SyncVariables(ctx context.Context, id int64, actor string) error {
	return p.svc.SyncPoolVariables(ctx, id, actor)
}

// renameScaleSet starts the scale-set demand again under the name in force.
type renameScaleSet struct{ dr *demandRunner }

func (p renameScaleSet) On() bool { return p.dr.ScaleSetOn() }
func (p renameScaleSet) Recreate(context.Context) error {
	p.dr.Restart()
	return nil
}

// renameAudit writes the rename's entries as the person who asked.
type renameAudit struct{ auth *auth.Service }

func (a renameAudit) Audit(ctx context.Context, actor, action, subject, detail string) {
	p := repos.System
	if actor != "" {
		p.Login = actor
	}
	a.auth.Audit(ctx, p, action, "pool", subject, detail)
}
