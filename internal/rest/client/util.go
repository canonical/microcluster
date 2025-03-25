package client

import (
	"context"
	"time"
)

// contextWithTimeout returns a context with a timeout, if the given context doesn't already have one.
func contextWithTimeout(ctx context.Context, timeout time.Duration) (context.Context, func()) {
	_, ok := ctx.Deadline()
	if ok {
		// Context already has a timeout. Return it as is, no need to cancel.
		return ctx, func() {}
	}

	return context.WithTimeout(ctx, timeout)
}
