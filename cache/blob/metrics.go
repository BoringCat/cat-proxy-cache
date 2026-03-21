package blob

import (
	"slices"
	"sync"

	"github.com/boringcat/cat-proxy-cache/utils"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	cache_key_length = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: "cat_proxy",
		Subsystem: "cache",
		Name:      "key_length",
		Help:      "缓存键的长度",
		Buckets:   slices.Collect(utils.AddRange[float64](16, 256, 16)),
	})
	once sync.Once
)

func init() {
	once.Do(func() {
		prometheus.MustRegister(cache_key_length)
	})
}
