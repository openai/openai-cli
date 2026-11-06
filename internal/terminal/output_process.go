package terminal

import "context"

type detachedOutputContextKey struct{}

// DetachedOutputContext opts the output helper into a separate session on
// supported platforms, so image diagnostics survive foreground-group signals.
func DetachedOutputContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, detachedOutputContextKey{}, true)
}
