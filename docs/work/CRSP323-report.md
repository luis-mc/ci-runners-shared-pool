# CRSP323 report: base image reused from ghcr.io

Branch CRSP323-integration, from origin/CRSP314-integration (PR 448 is still open; I needed its ci.yml and its browsers Dockerfile fix). Rebase onto main once 448 merges.

## What changed
- `scripts/ci/image_key.py` + `test_image_key.py` (14 tests, stdlib): `key` = SHA-256 over every file under `images/**` (path + content, text CRLF folded to LF, `images/linux-base/crsp` skipped, order-independent), PlaywrightPin from `internal/runners/layers.go`, arch, `--extra` pins, KEY_VERSION. `lookup` = anonymous `docker buildx imagetools inspect`, fail open to a build on any error.
- `ci.yml` image job (read-only, unchanged permissions): key, lookup, copy by digest into the job's local registry as `crsp-linux-base-tools:ci` (`imagetools create`, fallback pull/tag/push), skip the tools-stage build, log `reused image <digest> built by main (run <id>)`. The final base build gets `BASE_IMAGE=<local tools image>` on a hit, else `tools` (Dockerfile ARG, default keeps a plain `docker build` unchanged).
- `.github/workflows/base-image.yml` (new): the only writer, job-level `packages: write`, `if github.ref == refs/heads/main`. Push to main (paths images/**, layers.go) publishes `ghcr.io/<repo>/base-<arch>:<key>` when missing; weekly cron (Mon 03:17 UTC) and manual `fresh` rebuild with `no-cache` and replace the tag. Labels `org.crsp.built-by-ref`, `org.crsp.run-id`, `org.crsp.inputs-hash`.
- Guard: a hit counts only when the labels say refs/heads/main, a numeric run id, and the same key (`built_by_main`, tested). Pull requests never push (the PR job has no write scope and no login).
- Fixed the browsers Dockerfile from PR 448: it contained literal `\n` characters instead of line continuations in the mirror/retry loop, which the shell would read as `do n [ -f ...` (broken). Now real `\` continuations; mirror and retry logic kept.

## Scope note (what is reused)
The stored unit is the `tools` stage (apt, node, gh, Actions runner), the slow base. The layers (browsers, llvm, kvm, packaging) still build from the gha cache on top of it; the crsp binary stage is always built (seconds). Storing layers too would use the same key script with a per-layer name; not done, to keep size and scope small.

## Checks run
- `python3 -m unittest scripts/ci/test_image_key.py`: 14 pass. `go test ./images ./internal/runners`: pass (I first used `ARG TOOLS_BASE`; pins_test only allows `${BASE_IMAGE}`, so the ARG is `BASE_IMAGE`).
- prettier --check on both workflows: clean. actionlint/shellcheck not installed here; CI lint runs them.
- No docker run locally.

## Measure in the first main run (commands and expected output)
First run on main after merge: `base-image.yml` builds and pushes (cold, ~10 min per arch). Then on any release PR:
1. Step log "Look up the stored tools stage": `lookup ghcr.io/luis-mc/ci-runners-shared-pool/base-amd64:<64 hex>: N s`, then `hit=true`, `digest=sha256:...`, `run_id=<main run>`. Expect N under 5 s.
2. Step "Reuse the stored tools stage": `reused image sha256:... built by main (run <id>), copied in N s`. This is the pull time to compare with the tools-stage build (about 8-10 min cold). Also in the job summary.
3. By hand, anywhere with docker: `docker buildx imagetools inspect ghcr.io/luis-mc/ci-runners-shared-pool/base-amd64:<key> --format '{{json .Image}}'` shows `config.Labels` with the three org.crsp labels. Key: `python3 scripts/ci/image_key.py key --arch amd64 --extra "EXTRA_PACKAGES=zlib1g-dev libc++-dev"`.
4. Step "docker build base image": its log must show `FROM localhost:5000/crsp-linux-base-tools:ci` and no apt output.

## Risks / unknowns
- First publish creates a PRIVATE package: the owner sets it public once (package settings); until then PRs fail open to a build.
- `imagetools create` to `localhost:5000` (plain HTTP): fallback pull/tag/push is there; if only the fallback works, the digest may differ and downstream layer caches could miss on a hit.
- `{{json .Image}}` output shape differs between buildx versions; parser accepts a bare config or a platform map. Untested against real ghcr.
- Key counts only images/**, PlaywrightPin and arch; a change to apt packages' upstream versions arrives only via the weekly rebuild (up to 7 days stale by design).
- Release PRs that change images/** miss and build as today.
- Tag cleanup (old keys accumulate) is not done; add a prune step later.
- base-image.yml push trigger races the CI main run: until it finishes, PRs build.
