package cache

import "context"

type historyLenKey struct{}

// WithHistoryLen attaches the number of session-history messages
// SessionMiddleware prepended to the request, so the semantic cache can
// embed only the caller's actual new turn instead of the full
// history-plus-turn text. Without this, a rich injected history dominates
// the token-overlap score and can make two genuinely different questions
// in the same session score as a false SEMANTIC-HIT against each other —
// see docs/rfc/0001-semantic-cache.md Open Question #10.
func WithHistoryLen(ctx context.Context, n int) context.Context {
	return context.WithValue(ctx, historyLenKey{}, n)
}

// HistoryLenFromContext returns the history length set by WithHistoryLen,
// or (0, false) if none was set — callers should then treat the whole
// message list as new (today's behavior, unchanged when no session
// middleware is in play).
func HistoryLenFromContext(ctx context.Context) (int, bool) {
	n, ok := ctx.Value(historyLenKey{}).(int)
	return n, ok
}
