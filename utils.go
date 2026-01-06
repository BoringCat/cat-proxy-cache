package main

import (
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

func getTTLFromHeader(r *http.Request, resp *http.Response) (ttl time.Duration, err error) {
	s := r.Header.Get(fmt.Sprint("X-Cache-TTL-", resp.StatusCode))
	if len(s) > 0 {
		if ttl, err = time.ParseDuration(s); err == nil {
			return
		}
	}
	s = r.Header.Get(fmt.Sprint("X-Cache-TTL-", resp.StatusCode/100, "00"))
	if len(s) > 0 {
		if ttl, err = time.ParseDuration(s); err == nil {
			return
		}
	}
	s = r.Header.Get("X-Cache-TTL")
	if len(s) > 0 {
		return time.ParseDuration(s)
	}
	err = io.EOF
	return
}

func copyMap[K comparable, V any](dst map[K]V, src map[K]V) {
	for k, v := range src {
		dst[k] = v
	}
}

func copyHTTPHeader(w http.ResponseWriter, src http.Header) {
	for k, vs := range src {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
}

func orderValue[T any](values ...*T) *T {
	for _, val := range values {
		if val != nil {
			return val
		}
	}
	return nil
}

func waitAndCloseChannel[T any](wg *sync.WaitGroup, ch chan T) {
	wg.Wait()
	close(ch)
}
