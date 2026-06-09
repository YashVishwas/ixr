// Package modelperf stores and retrieves per-(model, intent) performance statistics.
// postgres is the source of truth; redis is the hot cache the scoring engine reads.
package modelperf

// PostgresStore is the source-of-truth adapter seam. A concrete sql.DB-backed
// implementation can fill this type without changing domain scoring code.
type PostgresStore struct{}
