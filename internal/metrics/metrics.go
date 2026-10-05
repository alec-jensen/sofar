// Package metrics exposes Prometheus metrics. Labels never carry amounts,
// descriptions, account ids or names. spec: S19.18.
package metrics

import (
	"net/http"
	"runtime/debug"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics holds the registry and the counters other packages update.
type Metrics struct {
	Registry *prometheus.Registry
	Jobs     *prometheus.CounterVec
}

// New builds a registry with build info and job counters.
func New(version string) *Metrics {
	reg := prometheus.NewRegistry()
	m := &Metrics{
		Registry: reg,
		Jobs:     prometheus.NewCounterVec(prometheus.CounterOpts{Name: "sofar_jobs_total", Help: "Jobs finished, by kind and result."}, []string{"kind", "result"}),
	}
	goVersion := "unknown"
	if bi, ok := debug.ReadBuildInfo(); ok {
		goVersion = bi.GoVersion
	}
	info := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "sofar_build_info", Help: "Build information."}, []string{"version", "go_version"})
	info.WithLabelValues(version, goVersion).Set(1)
	reg.MustRegister(info, m.Jobs)
	return m
}

// JobDone records a finished job attempt.
func (m *Metrics) JobDone(kind string, ok bool) {
	result := "ok"
	if !ok {
		result = "error"
	}
	m.Jobs.WithLabelValues(kind, result).Inc()
}

// Handler serves the registry in Prometheus text format.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.Registry, promhttp.HandlerOpts{})
}
