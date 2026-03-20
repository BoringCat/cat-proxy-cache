package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strings"
	"text/template"
	"time"
)

type CtxKey struct {
	name string
}

var (
	UpStreamURL = CtxKey{"UpStreamURL"}
	CacheKey    = CtxKey{"CacheKey"}
	RequestId   = CtxKey{"RequestId"}
)

type TemplateOpt struct {
	Upstream *url.URL
	Request  *http.Request
}

type ServerOpt struct {
	Vserver    *VServer
	Path       *Path
	Cache      *Cacher
	NoRedirect bool
}

func (opt *ServerOpt) getModel() *Model {
	return &Model{
		Upstream:       orderValue(opt.Path.Upstream, opt.Vserver.Upstream),
		TTL:            orderValue(opt.Path.TTL, opt.Vserver.TTL),
		FollowRedirect: orderValue(opt.Path.FollowRedirect, opt.Vserver.FollowRedirect),
		CacheKey:       orderValue(opt.Path.CacheKey, opt.Vserver.CacheKey),
		Redis:          orderValue(opt.Path.Redis, opt.Vserver.Redis),
	}
}

type CacheResponseWriter struct {
	w      http.ResponseWriter
	resp   *CachedResp
	buffer bytes.Buffer
	getTTL func(int) (time.Duration, error)
	ttl    time.Duration
	cached bool
}

func (w *CacheResponseWriter) Header() http.Header {
	return w.w.Header()
}

func (w *CacheResponseWriter) Flush() {
	if f, ok := w.w.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *CacheResponseWriter) Write(p []byte) (n int, err error) {
	if w.cached {
		w.buffer.Write(p)
	}
	return w.w.Write(p)
}

func (w *CacheResponseWriter) WriteHeader(statusCode int) {
	if ttl, err := w.getTTL(statusCode); err == nil {
		w.ttl = ttl
	}
	w.cached = w.ttl >= time.Second
	if w.cached {
		w.w.Header().Set("X-Cache-Status", "MISS")
	} else {
		w.w.Header().Set("X-Cache-Status", "BYPASS")
	}
	w.resp.Code = statusCode
	w.resp.Header = w.w.Header()
	w.w.WriteHeader(statusCode)
}

type RedirectTransport struct {
	next        http.RoundTripper
	maxRedirect int
}

func (t *RedirectTransport) Do(req *http.Request, retry int) (resp *http.Response, err error) {
	if resp, err = t.next.RoundTrip(req); err != nil {
		return
	}
	if resp.StatusCode > 300 && resp.StatusCode < 400 {
		if retry+1 >= t.maxRedirect {
			return
		}
		url, lerr := resp.Location()
		if lerr != nil {
			return
		}
		req = req.Clone(req.Context())
		req.URL = url
		resp, err = t.Do(req, retry+1)
	}
	return
}
func (t *RedirectTransport) RoundTrip(req *http.Request) (resp *http.Response, err error) {
	return t.Do(req, 0)
}

func newHTTPRoundTripper(redirect bool) http.RoundTripper {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = 10
	originDialContext := transport.DialContext
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := originDialContext(ctx, network, addr)
		logger.Info("发起上游连接", "network", network, "addr", addr, "RemoteAddr", conn.RemoteAddr(), "LocalAddr", conn.LocalAddr(), "err", err)
		return conn, err
	}
	if redirect {
		return &RedirectTransport{next: transport}
	}
	return transport
}

type Server struct {
	getUrl       func(*http.Request) (*url.URL, error)
	cacheKeyTmpl *template.Template
	logger       *slog.Logger
	ttlMap       map[int]time.Duration
	proxy        *httputil.ReverseProxy
	*ServerOpt
}

func NewServer2(opt ServerOpt) (obj *Server, err error) {
	model := opt.getModel()
	s := new(Server)
	s.ServerOpt = &opt
	s.logger = logger.With("logger", "server", "host", opt.Vserver.Host, "prefix", opt.Path.Prefix)
	s.ttlMap = map[int]time.Duration{}
	if opt.Vserver.TTL != nil {
		copyMap(s.ttlMap, *opt.Vserver.TTL)
	}
	if opt.Path.TTL != nil {
		copyMap(s.ttlMap, *opt.Path.TTL)
	}
	if model.CacheKey == nil {
		err = fmt.Errorf("没有配置缓存键模板")
		return
	}
	if model.Upstream == nil {
		err = fmt.Errorf("没有配置上游地址")
		return
	}
	if s.cacheKeyTmpl, err = template.New(fmt.Sprint(opt.Vserver.Host, opt.Path.Prefix)).Parse(*model.CacheKey); err != nil {
		return
	}
	if len(opt.Path.Expr) > 0 {
		var expr *regexp.Regexp
		if expr, err = regexp.Compile(opt.Path.Expr); err != nil {
			return
		}
		repl := opt.Path.Repl
		if len(repl) == 0 {
			repl = "$1"
		}
		logger.Debug("构建表达式", "expr", expr, "repl", repl)
		s.getUrl = func(r *http.Request) (*url.URL, error) {
			return url.Parse(fmt.Sprint(*model.Upstream, expr.ReplaceAllString(r.URL.String(), repl)))
		}
	} else {
		s.getUrl = func(r *http.Request) (*url.URL, error) {
			return url.Parse(fmt.Sprint(*model.Upstream, r.URL))
		}
	}

	s.proxy = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			upstream := pr.In.Context().Value(UpStreamURL).(url.URL)
			pr.Out.Host = upstream.Host
			pr.Out.URL.Scheme = upstream.Scheme
			pr.Out.URL.Opaque = upstream.Opaque
			pr.Out.URL.Host = upstream.Host
			pr.Out.URL.RawQuery = upstream.RawQuery
			pr.Out.URL.RawPath = upstream.RawPath
			pr.Out.URL.RawFragment = upstream.RawFragment
		},
		Transport: newHTTPRoundTripper(!opt.NoRedirect),
	}
	obj = s
	return
}

func (s *Server) HandlePrune(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, "[")
	defer fmt.Fprint(w, "]")
	enc := json.NewEncoder(w)
	deleted := false
	for key := range s.Cache.Scan(r.Context(), s.Vserver.Host, r.URL.Path) {
		logger.Debug("查询到键", "key", key)
		if deleted {
			fmt.Fprint(w, ",")
		}
		s.Cache.DeleteByKey(r.Context(), key)
		enc.Encode(strings.TrimPrefix(key, CacheIndexPrefix))
		deleted = true
	}
}

func (s *Server) executeTemplate(data TemplateOpt) (resp string, err error) {
	var buf bytes.Buffer
	if err = s.cacheKeyTmpl.Execute(&buf, data); err != nil {
		return
	}
	resp = buf.String()
	return
}

func (s *Server) HandleCache(w http.ResponseWriter, r *http.Request) (cacheKey string, resp *CachedResp) {
	upstream, ok := r.Context().Value(UpStreamURL).(url.URL)
	if !ok {
		s.logger.Warn("没有传入上游URL")
		http.Error(w, http.StatusText(http.StatusBadGateway), http.StatusBadGateway)
		return
	}
	cacheKey, err := s.executeTemplate(TemplateOpt{
		Upstream: &upstream,
		Request:  r,
	})
	if err != nil {
		logger.Warn("获取缓存键失败", "err", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	resp = s.Cache.GetCache(r.Context(), cacheKey)
	return
}

func (s *Server) getTTL(h http.Header) func(int) (time.Duration, error) {
	return func(statusCode int) (ttl time.Duration, err error) {
		duration := h.Get(fmt.Sprint("X-Cache-TTL-", statusCode))
		if len(duration) > 0 {
			if ttl, err = time.ParseDuration(duration); err == nil {
				return
			}
		}
		duration = h.Get(fmt.Sprint("X-Cache-TTL-", statusCode/100, "00"))
		if len(duration) > 0 {
			if ttl, err = time.ParseDuration(duration); err == nil {
				return
			}
		}
		duration = h.Get("X-Cache-TTL")
		if len(duration) > 0 {
			return time.ParseDuration(duration)
		}
		var ok bool
		if ttl, ok = s.ttlMap[statusCode]; !ok {
			ttl = s.ttlMap[0]
		}
		return
	}
}

func (s *Server) HandleProxy(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	upstream := ctx.Value(UpStreamURL).(url.URL)
	cacheKey := ctx.Value(CacheKey).(string)
	resp := CachedResp{}
	pw := CacheResponseWriter{
		w:      w,
		resp:   &resp,
		getTTL: s.getTTL(r.Header),
		cached: true,
	}
	defer func() {
		if err := recover(); err != nil {
			s.logger.Info("转发数据失败", "err", err, "path", r.URL.Path)
		}
	}()
	s.proxy.ServeHTTP(&pw, r)
	s.logger.Debug("数据处理完成", "statusCode", pw.resp.Code)
	if pw.cached && pw.buffer.Len() > 0 {
		resp.Data = pw.buffer.Bytes()
		s.Cache.SetCache(context.TODO(), s.Vserver.Host, upstream.Path, cacheKey, pw.resp, pw.ttl)
	}
}

func (s *Server) GetCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, cached := s.HandleCache(w, r)
		if cached != nil {
			for k, vs := range cached.Header {
				for _, v := range vs {
					w.Header().Add(k, v)
				}
			}
			w.Header().Set("X-Cache-Status", "HIT")
			w.WriteHeader(cached.Code)
			w.Write(cached.Data)
		} else {
			ctx := context.WithValue(r.Context(), CacheKey, key)
			r = r.WithContext(ctx)
			next.ServeHTTP(w, r)
		}
	})
}

func (s *Server) GetUpstream(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstream, err := s.getUrl(r)
		s.logger.Debug("获取上游URL", "upstream", upstream)
		if err != nil {
			s.logger.Error("获取上游URL失败", "err", err)
			http.Error(w, http.StatusText(http.StatusBadGateway), http.StatusBadGateway)
			return
		}
		ctx := context.WithValue(r.Context(), UpStreamURL, *upstream)
		r = r.WithContext(ctx)
		next.ServeHTTP(w, r)
	})
}
