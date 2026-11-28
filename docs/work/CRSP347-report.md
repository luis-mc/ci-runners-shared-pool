# CRSP347 report: import agent installers (console step + `crsp` command)

Status: IN PROGRESS.

## Done
- [x] Store: both name patterns (zip, tar.gz, msi, pkg, deb), `State`, `ErrSumsKnown`.
- [x] OpenAPI: `listAgentInstallers`, `importAgentInstaller`; codegen (Go server, client, TS).
- [x] Go handler, authz, wiring, tests.

## To do
- [x] CLI `crsp machine agent-installers list|import FILE...` + tests
- [ ] Console step + design-system pass
- [ ] Spec, change note, docs checks

## Open questions
- CRSP348 native package names: assumed `CrspAgent-<version>-<os>-<arch>.<msi|pkg|deb>` (one table, `formats` in agentinstallers.go).
