# Design system: next version (draft)

<!-- status:start -->

**Status:** draft. Not approved. Created on 2026-11-21 as a full copy of `v4`; the apps follow `v4` until the owner approves this draft.
<!-- status:end -->

> **Starter.** Every item in this version is a starter item. Keep, change or delete each one before v1 is approved.

This draft is a full, project-neutral starter design system. It holds UX rules, shared components, tokens and the words users see. The change list is in [CHANGES.md](CHANGES.md).

## Order of work

1. Look at the project's own styles. Replace the starter token values in [tokens.md](tokens.md) with the project's values only if the owner agrees.
2. Read [ux-rules.md](ux-rules.md). Keep, change or delete each rule. Add the project's own.
3. Read [components.md](components.md). Set each Source to a real path. Delete components the project does not need.
4. Read [content.md](content.md). Change the action words to the project's words.
5. Add a `CHANGES.md` entry for each change. Rebuild the gallery with `design_system.py visuals`.
6. Show the gallery to the owner. Promote only with the owner's recorded approval.

## What the apps do not follow yet

The changes of this version that the apps still have to build. One line per change, `- C<n>: what the apps must do`. Empty for a new version.

- C16: build the pane frame, reverse-video selection, key-hint bar and the six themes with their Settings choice (LOOK2).
- C148: change the table text of every list to the new UX-72: wrap, break, ellipsis plus tooltip and tap to expand (cell full-text, component 62); retire the `rowHeights` guard (LOOK40).
- C150: build the runner timeline, cell full-text and the stacked trend chart (components 61 to 63); the stat tile, ranked findings strip and bar list are built (LOOK41).
- C19: use the shared slot timeline (component 77) for every jobs-over-time chart; the apps pass rows, bars and `nowMs` only (LOOK58).
