package update

import "context"

// Fetch downloads a GitHub release asset to dst (temporary name, then rename,
// capped at 300 MiB). The controller uses it to hold the packages it hands to
// new machines (machines R5.65).
func Fetch(ctx context.Context, url, dst string) error { return httpFetch(ctx, url, dst) }

// AllowedURL says whether a release asset URL may be fetched: HTTPS to GitHub's own hosts.
func AllowedURL(raw string) bool { return allowedHost(raw) }
