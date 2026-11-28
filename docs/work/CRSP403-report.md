# CRSP403 report: Windows runner services stay up

Root cause: the 7 services (Automatic start) start at boot before crsp; the listener cannot list C:\ProgramData\crsp\runners (the group crsp-runners has no right on it) and exits, SCM 7023 "Incorrect function" (events 08:21:42 and 16:50:41, after unclean reboots: event 41 at 08:21:33 and 16:50:32). crsp's heal pass (R5.89/R5.156) would grant the right and restart them, but the controller's own agent (machines_wiring.go) was built without Runners, so the pass never ran on this PC. Fix: agent.Config.Healer, backoff, why, audit.
