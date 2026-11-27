# CRSP342: caches and package proxy, design options

Design options only. No pull request to main. Open `index.html` in a browser (self-contained).

## Recommendation

Option 1 for row V. The receiver, mirrors and wipe already live in the controller process and the failure rules are fixed, so a dedicated holder (option 2) buys little until a pool is large.

Row W: native npm and PyPI proxies on the same hub (option 1). Spike apt-cacher-ng from option 3 for apt, and write a small native sparse-index proxy for crates, then decide. Do not take containers for npm and PyPI: the metadata rewriting is small and keeps one budget.

Estimate: about 17.5 days for V and the npm and PyPI part of W; apt and crates later, about 5 days after the spike.

## Options

1. **One hub in the controller, one budget** (about 17.5 d (V 11, W 6.5); apt and crates add about 5 d later). The controller process serves the receiver, mirrors, registry mirror and a native npm and PyPI proxy from one store with one byte budget. Runners are pointed at it by environment at start.
2. **Dedicated cache-holder machines** (about 29 d (earlier estimate 24 d; the placement and per-OS work was undercounted)). Any machine can be made a cache holder. The controller only records who holds what and tells runners where to go; data never passes through the controller.
3. **Checked off-the-shelf services** (about 18 d (V 11, W 7)). The package proxies are well-known programs (Verdaccio for npm, devpi for PyPI, apt-cacher-ng for apt) run as pinned containers that crsp starts, points runners at, watches and wipes. The Actions cache and mirrors stay native.

## Screens

`option-N-1300.png` and `option-N-393.png` are real screens of the console, built from the shared parts in `packages/ui` with demo data behind the temporary query `?task=342&option=N` (code in `apps/web/src/temp`, marked TEMPORARY; delete before any merge).

Docs: none, design options only.
