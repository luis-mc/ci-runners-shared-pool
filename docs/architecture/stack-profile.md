# Stack Profile — CI Runners Shared Pool (`crsp`)

**Status:** Binding for this product
**Conforms to:** `STACKSPEC 8.1.0`, `AUTHSPEC none`
**Last reviewed:** `2026-11-13` by `Luis Carvalho`

This is the declared stack document for this product. It is authoritative for this product's technology choices.

The specs themselves are not vendored here. They are maintained in the `luismc-project-setup` plugin. This profile records only what is specific to this product, plus the spec versions it conforms to. Everything not recorded below is governed by the spec at the version named above.

CI Runners Shared Pool (CRSP) manages shared self-hosted GitHub Actions runners on the owner's own machines (Windows, Linux, macOS). One Go binary has the modes `controller`, `agent`, `tray` (planned) and CLI subcommands. The web console is a React single-page app built to static files and embedded in the binary.

> A placeholder in angle brackets is a conformance failure. None remain: the owner decisions were recorded on 2026-11-13.

---

## 1. Spec versions

| Spec | Version implemented | Notes |
|---|---|---|
| `STACKSPEC` | `8.1.0` | |
| `AUTHSPEC` | `none` | Authentication is GitHub App OAuth, built in milestone 1. `auth-setup` has not been run. |

## 2. Track and selections

| Role | Selection | Notes |
|---|---|---|
| Server language / runtime | Go 1.27.4 | Compiled to one static binary. See the language reason below. |
| API framework | `net/http` (Go 1.22+ routing) with `oapi-codegen` strict server | Generated from the hand-authored OpenAPI file. |
| Web framework | React 19 + React Router (SPA mode), built with Vite | Static files embedded with `go:embed`. |
| Hosting — API | Self-hosted single binary on the owner's machines | No cloud host. |
| Hosting — web | Same binary (embedded) | The controller serves the console. |
| Database engine | SQLite (WAL mode) | |
| Database host | Embedded in-process (`modernc.org/sqlite`, pure Go) | `CGO_ENABLED=0`. |
| Data access / ORM | `sqlc` over `database/sql` | Generated code lives in `internal/store/sqlcgen`. |
| Migration tool | `goose`, migrations embedded in the binary | Applied at controller start. |
| Queue substrate | Datastore-resident (`job_queue` table in the same SQLite database) | See section 11 and the derivation below. |
| Cache | none | |
| Object storage | none | |
| Mail provider | none | Capability gap, deferred to milestone 8 behind one adapter (section 14). |
| Error / crash tracker | Local only: structured logs, crash files, the `doctor` command | Capability gap with a control (section 14). |
| Uptime checker | The product's own `doctor` and notifications (milestone 8) | There is no hosted service to watch. |
| CI platform | GitHub Actions, GitHub-hosted runners only | `ci.yml`, `release.yml`, `build.yml`, `artifact-prune.yml`. |
| Package manager | pnpm (web), Go modules (server) | |
| Task runner | go-task, run as `go tool task` | Pinned in `go.mod`. `package.json` scripts are thin shims. |
| Lint / format | `gofmt` + golangci-lint (Go); ESLint + Prettier + `tsc` (web) | |
| Test runner | `go test`, Vitest, Playwright | |
| Boundary check | `depguard` inside golangci-lint (`.golangci.yml`) | Enforces `STACKSPEC 3.2` and `3.3`. |

Four roles are choices constrained by a property:

| Role | Selection | How it satisfies the property | Clause |
|---|---|---|---|
| API contract format | OpenAPI 3.1 (`packages/contract/openapi.yaml`) for the HTTP API; protobuf (`packages/contract/proto`) for the agent link | Go server from `oapi-codegen`; TypeScript client from `openapi-typescript` + `openapi-fetch`; Go agent link from `buf` with `protoc-gen-go` and `protoc-gen-connect-go` (connect-go). CI regenerates all of it and fails on drift. | `STACKSPEC 4.1` |
| Push transport | none | The product sends no push notifications. No device tokens exist. | `STACKSPEC 8.3` |
| Mail feedback loop | none | No mail is sent until milestone 8 (section 14). | `STACKSPEC 8.1` |
| Product analytics | none | No telemetry and no analytics. Nothing leaves the owner's machines except GitHub API traffic. | `STACKSPEC 10.3` |

**Why Go, not Rust.** Go has the official `github.com/actions/scaleset` client and `go-github`. The closest prior art (ARC, GARM) is written in Go. Iteration is faster, and cross-compiling without CGO is easy.

**Targets.** Windows, Linux and macOS, amd64 and arm64, from the first release.

**Tray.** Planned, not built. It will be a `tray` mode using `fyne.io/systray`: a notification-area icon on Windows, a menu-bar item on macOS, and a StatusNotifierItem / AppIndicator on Linux. The macOS tray needs CGO, so macOS builds run on macOS runners.

## 3. Pinned versions

The lockfiles (`go.sum`, `pnpm-lock.yaml`) are authoritative for libraries. These are the toolchain pins.

| Component | Version |
|---|---|
| Go | 1.27.4 (`go.mod` `toolchain` directive) |
| Node | 24 (CI uses the current 24.x; local 24.16.0) |
| pnpm | 10.34.5 (`packageManager` in `package.json`) |
| TypeScript | 5.9.3 (workspace override; TypeScript 7 breaks `openapi-typescript` and `typescript-eslint`) |
| React / React Router | 19.3.1 / 7.18.5 |
| Vite | 8.3.8 |
| Vitest / Playwright | 5.1.0 / 1.65.1 |
| ESLint / Prettier | 10.15.0 / 3.10.1 |
| go-task | 3.54.0 |
| oapi-codegen | 2.8.2 |
| sqlc | 1.31.1 |
| buf | 1.75.0 |
| connect-go | 1.21.1 |
| goose | 3.30.1 |
| modernc.org/sqlite | 1.62.0 |
| golangci-lint | 2.15.0 (pinned in `ci.yml`) |
| gitleaks | 8.30.1 (pinned with SHA-256 in `ci.yml`) |
| Swift / Xcode | not used |
| Kotlin / Gradle / AGP | not used |

## 4. Minimum supported OS versions

A product decision, not a default (`STACKSPEC 0.3`). Set by Luis Carvalho on 2026-11-13.

| Platform | Minimum | Set on | Review due |
|---|---|---|---|
| Windows | Windows 11 23H2+ and Windows Server 2022+ | 2026-11-13 | 2027-05-13 |
| Linux | Kernel 5.15+ with systemd (Ubuntu 22.04+, Debian 12+, Fedora 40+) | 2026-11-13 | 2027-05-13 |
| macOS | macOS 14+ (Apple Silicon for VMs) | 2026-11-13 | 2027-05-13 |
| Browsers (web console) | Last 2 versions of Chrome, Edge, Firefox and Safari | 2026-11-13 | 2027-05-13 |
| iOS | n/a | n/a | n/a |
| Android | n/a | n/a | n/a |

Mobile ships: no. The rules apply either way (`STACKSPEC 0.3`).

## 5. Environment inventory

| Tier | URLs | Datastore | Secrets scope |
|---|---|---|---|
| preview/dev | `http://127.0.0.1:8080` locally; per-OS CI artifacts for a pull request | SQLite file in `CRSP_DATA_DIR` | OS secret store of the developer's machine |
| staging | The `edge` pre-release, installed on the owner's own pool first | SQLite file on the owner's controller | OS secret store of that machine |
| production | The stable release (`v*` tag), installed by users on their own machines | SQLite file on each user's controller | OS secret store of each user's machine |

## 6. Provisioning ledger

Statuses only. Never record a credential value here.

| Service | Dev | Staging | Production | Stored in | Owner |
|---|---|---|---|---|---|
| Database (embedded SQLite) | have | have | have | Local file in `CRSP_DATA_DIR` | Each controller's operator |
| GitHub App | deferred | deferred | deferred | OS secret store (private key, webhook secret) | Each controller's operator |
| Code signing (Authenticode, Apple notarization) | deferred | deferred | deferred | not applicable yet | Luis Carvalho |
| Cache | not used | not used | not used | | |
| Object storage | not used | not used | not used | | |
| Mail provider | not used | not used | not used | | |
| Error tracker | not used | not used | not used | | |
| Push (APNs, FCM) | not used | not used | not used | | |

- **GitHub App.** Deferred to milestone 1 coding. Each controller creates its own App through the GitHub App manifest flow at first run. There is no shared App.
- **Code signing.** Deferred to milestone 8. Until then, releases carry SHA-256 sums and build provenance attestations (`actions/attest-build-provenance`).

## 7. Data-collection inventory

Location: `docs/reference/data-inventory.md`, created in milestone 1 with the first stored personal data (the GitHub OAuth login). The skeleton stores no personal data.

Personal data the controller database keeps (github-integration R6.10). Guard: `internal/store/retention_test.go` fails when either table gains a column.

| Table | Field | What it is | Why | Ends |
|---|---|---|---|---|
| `users` | `github_user_id` | GitHub numeric id | Identity (R5.9) | With the user's removal |
| `users` | `login` | GitHub login name | Shown in the console and audit | With the user's removal |
| `users` | `role` | owner, admin or viewer | Access (R5.10) | With the user's removal |
| `users` | `added_by` | Who added the user | Audit | With the user's removal |
| `users` | `added_at`, `last_sign_in_at` | Dates | Audit | With the user's removal |
| `sessions` | `id_hash` | Hash of the random cookie id; the id itself is never stored | Sign-in (R5.13) | At sign-out, user removal or role change |
| `sessions` | `csrf_hash` | Hash of the CSRF token | Request check | With the session |
| `sessions` | `github_user_id`, `source`, `created_at`, `last_seen_at`, `ended_at` | Owner of the session, web or tray, dates | Sign-in | With the session |

No email address, name, avatar or GitHub token is kept in these two tables. Rows that hang off a user and go with them: `user_preferences` (theme), `cli_tokens` (hash and name), `link_codes` (hash), `tray_bindings` (OS user id of a machine).

## 8. Data residency

| Vendor | Processing region | Transfer basis |
|---|---|---|
| GitHub (API, Actions) | GitHub's, per the owner's GitHub account | The owner's own GitHub agreement |

Jurisdictional boundary this product commits to: all product data stays on the operator's own machines. The only outbound traffic is to GitHub. There is no telemetry.

## 9. Resilience

Backup, restore and the doctor command are designed in milestone 8. Until then the database is one SQLite file that the operator copies.

| Measure | Value |
|---|---|
| RPO — acceptable data loss | 24 h (daily automatic backup) |
| RTO — acceptable downtime | 1 h (reinstall and restore) |
| PITR retention window | none (SQLite has no point-in-time recovery; see section 14) |
| Backup retention window | 14 days |
| Last successful restore drill | none yet. The first drill is required before the first release (milestone 1). |

## 10. Classification and retention

Field classification: `docs/reference/data-classification.md`, created in milestone 1 with the first table that holds personal data.
Per-table retention declarations: `docs/reference/data-retention.md`, created at the same time.

## 11. Queue economics

| Property | Value |
|---|---|
| Substrate | datastore-resident: the `job_queue` table in the controller's SQLite database |
| Trigger | A job is enqueued in the same transaction as the change that needs it |
| Drain schedule | Decided with the worker in milestone 1 (only the table exists now) |
| Batch size | Decided with the worker in milestone 1 |
| Steady-state load | Low: one operator's pool, tens of jobs per minute at most |

**Queue derivation (`STACKSPEC 7.2`).** SQLite embedded in one process gives real interactive transactions, so transactional enqueue holds. The `sqlite_family` rating of `partial` applies to D1 and remote libSQL, which this product does not use. No outbox is needed.

## 12. Certificate pinning

Not applicable: no mobile apps.

| Platform | Pins? | Rationale | Rotation procedure |
|---|---|---|---|
| iOS | no | not shipped | n/a |
| Android | no | not shipped | n/a |

## 13. Assurance cadence

| Item | Value |
|---|---|
| Dependency-advisory triage window | Critical and high within 2 days; others within 7 days |
| Security review / pen-test cadence | `/luismc-audits:security-audit` at every milestone release |
| Last review | none yet |
| Vulnerability disclosure contact | GitHub private vulnerability reporting (see `SECURITY.md`) |

CI runs `govulncheck` and `pnpm audit --prod` on every pull request, and gitleaks over full history.

### Release mapping (`STACKSPEC 13.2`)

| Spec tier | This product |
|---|---|
| Per-PR preview | Per-OS CI artifacts (`crsp-<os>-<arch>`) |
| Staging | The `edge` pre-release, published on every merge to `main` and installed on the owner's pool first |
| Production | The stable release created by a `v*` tag |

## 14. Capability gaps

| Clause | Capability missing | Compensating control | Approved by | Date |
|---|---|---|---|---|
| `STACKSPEC 5.6` | Statement timeout (SQLite has none) | Every query runs under a context deadline (`store.DefaultDeadline`, 10 s, when the caller sets none) and the connection sets `busy_timeout` (5 s) and WAL mode | Luis Carvalho | 2026-11-13 |
| `STACKSPEC 5.5` | Point-in-time recovery | Operator-driven file backups, designed in milestone 8 | Luis Carvalho | 2026-11-13 |
| `STACKSPEC 8.1` | Mail provider | No mail is sent until milestone 8; then one adapter module (`internal/mail`) | Luis Carvalho | 2026-11-13 |
| `STACKSPEC 10.1` | Hosted error tracking | Structured logs, crash files and the `doctor` command; no data leaves the machine | Luis Carvalho | 2026-11-13 |
| `STACKSPEC 13.2` | Per-pull-request preview deployment | Per-OS CI artifacts for each pull request; `edge` as staging (section 13) | Luis Carvalho | 2026-11-13 |

## 15. Deviations

| Clause | Level | What we do instead | Reason | Approved by | Date |
|---|---|---|---|---|---|
| `STACKSPEC 13.5`, `13.6` | MUST (waived) | Standalone hooks (`.githooks/pre-commit`, `pre-push`, `commit-msg`) and a standalone `ci.yml`. No `luismc-delivery` branch-gate scripts. | The product replaces that plugin's runner-pool feature and must not depend on it. | Luis Carvalho | 2026-11-13 |

Control for the deviation: the pre-push hook runs the fast gates (gofmt, `go vet`, golangci-lint when installed, `go test` of changed packages, `prettier --check` of changed files, web typecheck), and the required CI check runs all twelve gates.
