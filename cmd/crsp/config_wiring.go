package main

import (
	"context"

	"github.com/luis-mc/ci-runners-shared-pool/internal/api"
	"github.com/luis-mc/ci-runners-shared-pool/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/internal/configio"
	"github.com/luis-mc/ci-runners-shared-pool/internal/notify"
	"github.com/luis-mc/ci-runners-shared-pool/internal/repos"
	"github.com/luis-mc/ci-runners-shared-pool/internal/tlsca"
)

// newConfigAsCode is config as code (operations R5.28 to R5.30) over the
// services that own each part of the setup. The engine adds no write path of
// its own: every change goes through the service, so its ranges, its audit and
// its drain apply.
func newConfigAsCode(d controllerDeps, version string, rsvc *repos.Service, classes api.RunnerClassService, notifier *notify.Service) *configio.Engine {
	who := func(ctx context.Context) (audit.Actor, auth.Principal) {
		p, _ := api.PrincipalFrom(ctx)
		return audit.ActorFrom(ctx), p
	}
	st := d.Ops.Settings
	return &configio.Engine{
		Version: version,
		Audit:   d.Ops.Audit,
		Sections: []configio.Section{
			configio.NewSettings(st, who),
			configio.NewPool(st, who),
			configio.NewQueue(st, who),
			configio.NewRunnerClasses(classes, who),
			configio.NewMachines(d.Machines, who),
			configio.NewImageLayers(d.Machines, st, who),
			configio.NewRepositories(rsvc, who),
			configio.NewNotifications(notifier, who),
		},
		Secrets: configio.SecretLister(d.MachineSecrets, []configio.NamedSecret{
			{Name: "Controller certificate authority key", Key: tlsca.CAKeySecret},
			{Name: "Controller server key", Key: tlsca.ServerKeySecret},
		}),
	}
}
