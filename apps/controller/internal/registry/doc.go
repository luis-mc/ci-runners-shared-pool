// Package registry is crsp's private image registry (docs/features/registry,
// RES20, owner decisions of 19 Nov 2026).
//
// The registry is embedded in the controller with the Distribution v3 library
// and served on the controller's HTTPS listener under /v2/. It is never public:
// the listener follows the private LAN address rule of lancache. Machines
// push only to crsp/incoming/<machine id>/...; the controller checks digest,
// labels, key and that the tag is new, then promotes by a manifest copy to
// crsp/std/<profile> or crsp/custom/<owner>-<repo>/<profile>. Tags are
// immutable. Tokens are issued by the controller over the agent link for 10
// minutes; no job ever sees a registry credential.
//
// This package holds the shared contract of the REG wave (REG10): the image
// key (key.go, the only behaviour here), the types every task uses (types.go)
// and the interfaces each task implements or calls (interfaces.go). Each
// interface names its owning task; a task implements it in its own files and
// tests against fakes of the others (the ownership map in the REG10 pull
// request and docs/features/registry/spec.md section 12).
package registry
