---
change: minor
task: CRSP361
---
New proposed spec: crsp starts and stops AWS EC2 runners for overflow in the owner's own account, with IAM Roles Anywhere and crsp's own CA (1-hour credentials), a tag-scoped EC2 policy, a 7-check Test connection, crsp caps before AWS Budgets, a per-system key store (DPAPI, Keychain, Secret Service with a systemd-creds or 0600 fallback), JIT configs from the manifest App, and a stack file the console hands over so no vendor AWS account is needed. Not built.
