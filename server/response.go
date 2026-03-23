package server

import (
	"bytes"
	"net/http"
	"strconv"
	"time"

	"github.com/boringcat/cat-proxy-cache/cache"
)

type CacheResponseWriter struct {
	w http.ResponseWriter

	statusCode int
	header     http.Header
	buffer     bytes.Buffer

	getTTL func(int) (time.Duration, error)
	ttl    time.Duration
	cached bool
}

func (w *CacheResponseWriter) makeData() *cache.CacheData {
	return &cache.CacheData{
		Code:   w.statusCode,
		Header: w.header,
		Data:   w.buffer.Bytes(),
	}
}

func (w *CacheResponseWriter) ContentLength() int64 {
	clHeader := w.w.Header().Get("Content-Length")
	switch {
	case clHeader != "":
		cl, err := strconv.ParseInt(clHeader, 10, 64)
		if err != nil || cl < 0 {
			return -1
		}
		return cl
	default:
		return -1
	}
}

func (w *CacheResponseWriter) Header() http.Header {
	return w.w.Header()
}

func (w *CacheResponseWriter) Flush() {
	if f, ok := w.w.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *CacheResponseWriter) setCacheStatus(statusCode int) {
	if ttl, err := w.getTTL(statusCode); err == nil {
		w.ttl = ttl
	}
	w.cached = w.ttl >= time.Second
	if w.cached {
		w.w.Header().Set("X-Cache-Status", "MISS")
	} else {
		w.w.Header().Set("X-Cache-Status", "BYPASS")
	}
	w.statusCode = statusCode
	w.header = w.w.Header()
}

func (w *CacheResponseWriter) Write(p []byte) (n int, err error) {
	if w.statusCode == 0 {
		w.setCacheStatus(http.StatusOK)
	}
	if w.cached {
		w.buffer.Write(p)
	}
	return w.w.Write(p)
}

func (w *CacheResponseWriter) WriteHeader(statusCode int) {
	w.setCacheStatus(http.StatusOK)
	w.w.WriteHeader(statusCode)
}
