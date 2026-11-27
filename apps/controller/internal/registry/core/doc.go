// Package core is the registry core of the controller (docs/features/registry,
// REG1, R5.1 to R5.12): the embedded Distribution v3 mounted under /v2/, the
// token issuer, staging and promotion, and the agent link sink for tokens and
// push reports.
//
// The registry is never public. It is mounted only on a listener whose address
// follows the private LAN rule of lancache (CheckListen), it refuses a peer
// that is not on a private, loopback or link-local address, and it answers
// every request without a valid token with 401 (anonymous reads included).
package core
