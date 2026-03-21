package server

import (
	"context"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"slices"
	"strings"
)

type redirectTransport struct {
	next        http.RoundTripper
	logger      *slog.Logger
	maxRedirect int
}

func (t *redirectTransport) Do(req *http.Request, retry int) (resp *http.Response, err error) {
	t.logger.Debug("发起上游请求", "retry", retry, "maxRedirect", t.maxRedirect, "url", req.URL, "header", req.Header)
	if resp, err = t.next.RoundTrip(req); err != nil {
		t.logger.Debug("上游请求异常", "err", err)
		return
	}
	t.logger.Debug("上游返回", "url", req.URL, "resp", resp.Status)
	if resp.StatusCode > 300 && resp.StatusCode < 400 {
		if retry+1 >= t.maxRedirect {
			return
		}
		defer resp.Body.Close()
		url, lerr := resp.Location()
		if lerr != nil {
			return
		}
		t.logger.Debug("上游返回跳转", "url", url)
		req = req.Clone(req.Context())
		req.URL = url
		resp, err = t.Do(req, retry+1)
	}
	return
}

func (t *redirectTransport) RoundTrip(req *http.Request) (resp *http.Response, err error) {
	return t.Do(req, 0)
}

func newHTTPRoundTripper(maxRedirect int) http.RoundTripper {
	logger := slog.With("logger", "transport")
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = 10
	originDialContext := transport.DialContext
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := originDialContext(ctx, network, addr)
		logger.Info("发起上游连接", "network", network, "addr", addr, "RemoteAddr", conn.RemoteAddr(), "LocalAddr", conn.LocalAddr(), "err", err)
		return conn, err
	}
	return &redirectTransport{next: transport, maxRedirect: maxRedirect, logger: logger}
}

func proxyRewrite(pr *httputil.ProxyRequest) {
	upstream := pr.In.Context().Value(UpStreamURL).(*url.URL)
	for _, key := range slices.Collect(maps.Keys(pr.Out.Header)) {
		if strings.HasPrefix(strings.ToLower(key), "x-cache-") {
			pr.Out.Header.Del(key)
		}
	}
	pr.SetURL(upstream)
	pr.Out.URL.Path, pr.Out.URL.RawPath, pr.Out.Host = upstream.Path, upstream.RawPath, upstream.Host
	pr.Out.Header.Set("Host", upstream.Host)
}

var cachedProxy = map[int]*httputil.ReverseProxy{}

func getProxy(maxRedirect int) *httputil.ReverseProxy {
	if p, ok := cachedProxy[maxRedirect]; !ok {
		cachedProxy[maxRedirect] = &httputil.ReverseProxy{
			Rewrite:   proxyRewrite,
			Transport: newHTTPRoundTripper(maxRedirect),
		}
		return cachedProxy[maxRedirect]
	} else {
		return p
	}
}
