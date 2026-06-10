package observability

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics holds all Prometheus instruments for the ixr proxy.
type Metrics struct {
	RequestsTotal *prometheus.CounterVec
	LatencyHist   *prometheus.HistogramVec
	TokensIn      *prometheus.CounterVec
	TokensOut     *prometheus.CounterVec
	CacheHits     prometheus.Counter
	CacheMisses   prometheus.Counter
	CBOpen        *prometheus.GaugeVec
}

// NewMetrics registers and returns all ixr Prometheus metrics.
// Call once at startup; pass the returned registry to promhttp.HandlerFor.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}
	m := &Metrics{
		RequestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ixr_requests_total",
			Help: "Total LLM requests routed by ixr.",
		}, []string{"provider", "model", "status"}),

		LatencyHist: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "ixr_request_duration_seconds",
			Help:    "End-to-end request latency in seconds.",
			Buckets: prometheus.ExponentialBuckets(0.05, 2, 10),
		}, []string{"provider", "model"}),

		TokensIn: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ixr_tokens_in_total",
			Help: "Total prompt tokens sent to providers.",
		}, []string{"provider", "model"}),

		TokensOut: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ixr_tokens_out_total",
			Help: "Total completion tokens received from providers.",
		}, []string{"provider", "model"}),

		CacheHits: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "ixr_cache_hits_total",
			Help: "Semantic cache hits (requests served from cache).",
		}),

		CacheMisses: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "ixr_cache_misses_total",
			Help: "Semantic cache misses.",
		}),

		CBOpen: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "ixr_circuit_breaker_open",
			Help: "1 when the circuit breaker for a model is open.",
		}, []string{"model"}),
	}

	for _, c := range []prometheus.Collector{
		m.RequestsTotal, m.LatencyHist,
		m.TokensIn, m.TokensOut,
		m.CacheHits, m.CacheMisses,
		m.CBOpen,
	} {
		reg.MustRegister(c)
	}
	return m
}

// Record emits the standard set of metrics after a completed request.
func (m *Metrics) Record(provider, model string, statusCode int, latency time.Duration, tokIn, tokOut int) {
	lp := prometheus.Labels{"provider": provider, "model": model}
	m.RequestsTotal.With(prometheus.Labels{
		"provider": provider,
		"model":    model,
		"status":   strconv.Itoa(statusCode),
	}).Inc()
	m.LatencyHist.With(lp).Observe(latency.Seconds())
	if tokIn > 0 {
		m.TokensIn.With(lp).Add(float64(tokIn))
	}
	if tokOut > 0 {
		m.TokensOut.With(lp).Add(float64(tokOut))
	}
}

// Handler returns an HTTP handler for the /metrics endpoint.
func Handler(gatherer prometheus.Gatherer) http.Handler {
	if gatherer == nil {
		gatherer = prometheus.DefaultGatherer
	}
	return promhttp.HandlerFor(gatherer, promhttp.HandlerOpts{})
}
