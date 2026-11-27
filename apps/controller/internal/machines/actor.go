package machines

import "context"

type actorKey struct{}

// WithActor names who acts, for the audit log (R6.4). The API's session check
// sets it; nothing here trusts it for authorization.
func WithActor(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, actorKey{}, name)
}

// ActorFrom returns the actor of ctx, or "unknown".
func ActorFrom(ctx context.Context) string {
	if v, ok := ctx.Value(actorKey{}).(string); ok && v != "" {
		return v
	}
	return "unknown"
}
