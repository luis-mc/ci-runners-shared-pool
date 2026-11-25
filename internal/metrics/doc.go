// Package metrics exports the pool's numbers to tools the owner already uses
// (observability R5.40 to R5.47): a Prometheus /metrics page, and an
// OpenTelemetry (OTLP over HTTP) push of the same metric set.
//
// Both are off until the owner turns them on. The page listens on 127.0.0.1
// unless the owner chooses to expose it, and it can ask for a bearer token.
// Nothing here reads another package directly: a Source gives the numbers.
//
// The code is pure Go with no operating-system calls, so it builds and its
// unit tests run on Windows, Linux and macOS alike.
package metrics
