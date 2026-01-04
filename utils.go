package main

import (
	"fmt"
	"io"
	"iter"
	"net/http"
	"runtime"
	"sync"
	"time"

	"github.com/miekg/dns"
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

func doDnsQuery(client *dns.Client, wg *sync.WaitGroup, ch chan<- dns.RR, domain string, dnsType uint16, server string) {
	defer wg.Done()
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(domain), dnsType)
	r, _, err := client.Exchange(m, server)
	if err != nil || r.Rcode != dns.RcodeSuccess {
		return
	}
	for _, rr := range r.Answer {
		ch <- rr
	}
}

func doDualTypeQuery(client *dns.Client, domain, server string) iter.Seq[dns.RR] {
	ch := make(chan dns.RR, runtime.GOMAXPROCS(0))
	var wg sync.WaitGroup
	wg.Add(2)
	go waitAndCloseChannel(&wg, ch)
	go doDnsQuery(client, &wg, ch, domain, dns.TypeA, server)
	go doDnsQuery(client, &wg, ch, domain, dns.TypeAAAA, server)
	return func(yield func(dns.RR) bool) {
		next := true
		for addr := range ch {
			if !next {
				continue
			}
			next = next && yield(addr)
		}
	}
}

func doSrvQuery(client *dns.Client, domain, server string) iter.Seq2[dns.RR, uint16] {
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(domain), dns.TypeSRV)
	resp, _, err := client.Exchange(m, server)
	if err != nil || resp.Rcode != dns.RcodeSuccess {
		return func(yield func(dns.RR, uint16) bool) {}
	}
	return func(yield func(dns.RR, uint16) bool) {
		for _, rr := range resp.Answer {
			if srv, ok := rr.(*dns.SRV); ok {
				if _, ok := dns.IsDomainName(srv.Target); ok {
					for rr := range doDualTypeQuery(client, srv.Target, server) {
						if !yield(rr, srv.Port) {
							return
						}
					}
				} else {
					if !yield(srv, srv.Port) {
						return
					}
				}
			}
		}
	}
}
