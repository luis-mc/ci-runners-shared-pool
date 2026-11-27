# CRSP343: installers and release notes, design options

Design options only. No pull request to main. Open `index.html` in a browser (self-contained).

## Recommendation

Option 3 now, option 1 later. Release notes help every update from the next release on, need no money or lead time, and use a field the signed manifest can carry.

Start the certificate orders in parallel because their lead time is the long pole, then do option 1 when they arrive. Option 2 comes after option 1, since winget and Homebrew need a signed installer.

Estimate: about 5 days now, about 8 days plus certificate lead time later.

## Options

1. **Sign and notarize, show trust per installer** (about 8 d, plus certificate lead time of 3 to 10 days). Get a Windows code-signing certificate and an Apple Developer ID, turn SIGNING_ENABLED on, add notarization, make the updater and install script check the signature, and show the trust of each installer in the console.
2. **Package repositories, with a switch for who updates** (about 14 d, and winget and Homebrew wait for option 1). Publish an apt repository, a yum repository, a Homebrew tap and a winget manifest, and let the owner choose per machine whether crsp updates itself or the system package manager does.
3. **Release notes first, owner-facing notes in console and tray** (about 5 d). Write a short owner-facing note for each release, carry it in the signed update manifest, and show it in the console and the tray before and after an update. Installers stay as they are.

## Screens

`option-N-1300.png` and `option-N-393.png` are real screens of the console, built from the shared parts in `packages/ui` with demo data behind the temporary query `?task=343&option=N` (code in `apps/web/src/temp`, marked TEMPORARY; delete before any merge).

Docs: none, design options only.
