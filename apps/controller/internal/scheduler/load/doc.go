// Package load measures the load of this machine for the gate: memory use,
// CPU use, the 10 s sample reuse and 30 s smoothing, the figure the agent
// publishes for readers in a VM or container, and the top processes by name
// (scheduling R5.40 to R5.46). It only reads; it never signals a process (R5.27).
//
// Ported from dev-standards claude/plugins/delivery/scripts/load.py.
package load
