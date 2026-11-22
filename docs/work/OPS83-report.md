# OPS83 report: machine health and job stats

Branch OPS83-machine-health. Back end only.

## Steps
- [x] 1. HealthReport proto (field 32 of SessionRequest) and codegen.
- [x] 2. internal/agent/health collectors (per OS, fakes) and agent wiring.
- [x] 3. Migration 00040, store queries, job stats.
- [x] 4. internal/machinehealth controller service and thresholds.
- [ ] 5. OpenAPI and handlers.
- [ ] 6. Doctor checks.
- [ ] 7. Docs, gates, PR, CI.
