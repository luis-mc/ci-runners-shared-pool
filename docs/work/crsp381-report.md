# CRSP381 report
1. addresses guard: three npm/PyPI upstream defaults moved to apps/controller/internal/addresses/addresses.go; lancache/pkgproxy.go:38-40 aliases them.
2. debugaccess: each test migrated a fresh store (~1.5 s under -race, 32 s total vs 10 s unit limit). helpers_test.go now migrates once and copies the file. 5 s total.
3. go-test.sh: go-tiers `chunks` prints "pkg count index"; loop read count="N k" -> now reads k and runs only that chunk.
4. LC-11: LoadSecret already keeps an existing key; the file was ADDED by the upgrade (N-1 had none). windows.ps1 allows adding mirrors\packages.key, not changing it. Spec R5.125 + change note.
5. visuals regenerated; docs_check --strict 0/0.

## Round 2

Failure A (citation guard, DS234): `citation-test.mjs` accepts a backticked existing test path on the line with the rule ID, or a `Proof: IDs `path`` line. Added Proof lines: caches (CORE-5 -> ux-routes.spec.ts, its `primary` check runs on every route), github-integration (CORE-1/CORE-7/UX-2 -> design-system-follow.test.ts, UX-2 -> raw-controls.test.ts, UX-71 -> action-bar.test.ts), web-console (CORE-6 -> wizard-behaviour.test.ts, UX-88/89/90 -> LiveProgress.test.tsx; relabelled its weighted-bar describe UX-88 to UX-89), debug-access (section 10: design-system-follow, ux-routes, ux-static, wizard-behaviour; the Debug card is not built, the lines name shared guards that cover every route). Guard prints ok; docs_check --strict 0/0.

Failure B: sticky action bar overlaps key-hint bar by 2 px at 360 px on Linux. Cause (not reproducible on Windows): the bar offset is the JS-measured `--ds-keyhints-height`, written only by a ResizeObserver (after layout); a font load or resize landing between frames leaves it stale for a frame when the walk forces layout. Fix in packages/ui/src/components/KeyHints.tsx (measureHeight): also write on window resize and document.fonts loadingdone/ready, observe border-box, stop writing after unmount.
