## Upgrade notes

- **The parity tokens are deleted on upgrade.** The parity check is gone (R5.85), so the upgrade drops the table that held its scoped access tokens (migration 00042). Nothing else you keep is touched. A token you made for the parity workflow stops working; there is nothing to move.
- The legacy `legacy_vars` setting is removed from the saved repository state on upgrade (migration 00043). Every repository keeps its other choices.
