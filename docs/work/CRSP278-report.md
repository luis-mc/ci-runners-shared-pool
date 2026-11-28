# CRSP278 report: dependency install for machines without internet (RELEASE-73, plan row P, M3, DEP15)

Branch `CRSP278-offline-deps`, base `CRSP383-integration`. Rows O (CRSP265/270) and Y are merged (registry: Live; code: `depsetup`, `agentinstallers` present).

## What changed

- `apps/controller/internal/depstore` (new): the controller's own copy of what a machine installs. Fetch to a `.part` file, SHA-256 check against the pin of this release, keep only a match; stored file re-checked once per process (a tampered one is dropped); import with the same check and a 2 GiB limit; `Service` adds the agent packages of `agentinstallers`. Catalog: pinned downloads, runner pins (`runners.AllPins`). Wiring: `cmd/crsp/depstore_wiring.go`.
- Serving: `/dep-files/<id>` on the :8461 agent link (`machines.SetDepFilesHandler`), TLS 1.3 + pinned certificate.
- Agent: `install.Config.FromController` - controller first, pinned SHA-256 checked, a wrong copy is thrown away, then the vendor; offline with no copy fails and names Downloads in the console.
- API: `GET /api/v1/dependencies/store`, `POST .../{id}/fetch`, `PUT .../{id}/file` (admin; audit `dependency.store.fetch|import`). CLI: `crsp dependencies [fetch ID | import ID FILE]`.
- Console: tab Downloads in Images and caches (`screens/dependencies/Downloads.tsx`): state of each dependency in words, Download now, Import a file.
- Docs: spec `dependency-setup` (DEP15, D-DEP-8, screens, built / not built, proof), change note CRSP278, next-version content.md "Downloads words" + CHANGES.

## Tests (all green)

- `go test -race`: depstore, api, cli, machines (new TLS serve test), cmd/crsp, agent/..., agent/install (4 new). golangci-lint on the touched packages: 0 issues.
- vitest: controller-console 901 passed (5 new in `Downloads.test.tsx`), `scripts/design` 137 passed, citation-test ok. eslint and prettier clean.
- Playwright (local Chromium): `design-layout` for images, caches, downloads at 360/393/1300 px: 3 passed.
- docs_check --strict: 0 errors, 0 warnings. codegen: no drift.

## Design system checklist

- CORE-1, CORE-3, CORE-4 checked; design-system v4 followed; next-version content.md gets the new words (UX-13, UX-38, UX-58). Shared parts: Card, CardGrid, Facts, Status, FileField, Button, Message, Section, Stack, Empty, Load. No app CSS, no `style=`, no primary outside the action bar. Not a wizard.

## Not built (open for the owner)

- Installs by package manager (Docker Engine by apt/dnf, Docker in WSL2, WSL2): need the machine's own network or a mirror.
- The agent does not yet ask the controller for the runner package (the controller keeps it; the machine still downloads it itself).
- Signature check beyond SHA-256; toolchains (the spec lists none).
- Docker Desktop has no URL/SHA-256 in `pinned.json` (empty today), so it shows "Cannot be checked in this release" until a release pins it.
