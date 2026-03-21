package index

import (
	"slices"
	"sync"

	"github.com/boringcat/cat-proxy-cache/utils"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	prune_scan_histogram = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "cat_proxy",
		Subsystem: "cache",
		Name:      "prune_scan_seconds",
		Help:      "扫描键的耗时",
		Buckets:   slices.Collect(utils.MultipRange(0.001, 1.024, 2)),
	}, []string{"command"})
	once sync.Once
)

func init() {
	once.Do(func() {
		prometheus.MustRegister(prune_scan_histogram)
	})
}
