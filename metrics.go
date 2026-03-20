package main

import (
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	request_time = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "cat_proxy",
		Subsystem: "cache",
		Name:      "request",
		Help:      "请求耗时直方图",
	}, []string{"method", "host", "path", "status_code", "cached"})
	response_data = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "cat_proxy",
		Subsystem: "cache",
		Name:      "response_bytes",
		Help:      "返回的数据量",
	}, []string{"method", "host", "path", "status_code", "cached"})
	cached_total = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "cat_proxy",
		Subsystem: "cache",
		Name:      "cached_total",
		Help:      "命中缓存的数量",
	}, []string{"method", "host", "path", "status_code"})
	cache_key_length = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: "cat_proxy",
		Subsystem: "cache",
		Name:      "key_length",
		Help:      "缓存键的长度",
		Buckets:   slices.Collect(addRange[float64](16, 256, 16)),
	})
	prune_scan_histogram = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: "cat_proxy",
		Subsystem: "cache",
		Name:      "prune_scan_seconds",
		Help:      "扫描键的耗时",
		Buckets:   slices.Collect(multipRange(0.001, 1.024, 2)),
	})
)

type responseRecorder struct {
	http.ResponseWriter
	StatusCode int
	Writed     int
}

func (r *responseRecorder) WriteHeader(code int) {
	r.StatusCode = code
	r.ResponseWriter.WriteHeader(code)
}
func (r *responseRecorder) Write(b []byte) (int, error) {
	if r.StatusCode == 0 {
		r.StatusCode = 200
	}
	n, err := r.ResponseWriter.Write(b)
	r.Writed += n
	return n, err
}

func prometheusMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lbs := prometheus.Labels{}
		lbs["method"] = r.Method
		route := mux.CurrentRoute(r)
		lbs["host"], _ = route.GetHostTemplate()
		lbs["path"], _ = route.GetPathTemplate()
		rw := responseRecorder{w, 0, 0}
		begin := time.Now()
		next.ServeHTTP(&rw, r)
		observer := time.Since(begin)
		lbs["status_code"] = fmt.Sprint(rw.StatusCode)
		cached := w.Header().Get("X-Cache-Status")
		if len(cached) == 0 {
			cached = "MISS"
		}
		if cached == "HIT" {
			cached_total.With(lbs).Add(1)
		}
		lbs["cached"] = cached
		request_time.With(lbs).Observe(observer.Seconds())
		response_data.With(lbs).Add(float64(rw.Writed))
	})
}

func startMetricServer() {
	if err := http.ListenAndServe(metricAddr, promhttp.Handler()); err != nil {
		panic(err)
	}
}
