package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/gorilla/mux"
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
	logger      *slog.Logger
	maxRedirect int
}

func (t *RedirectTransport) Do(req *http.Request, retry int) (resp *http.Response, err error) {
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
		return &RedirectTransport{next: transport, maxRedirect: 10, logger: logger.With("logger", "transport")}
	}
	return &RedirectTransport{next: transport, maxRedirect: 0, logger: logger.With("logger", "transport")}
}

func proxyRewrite(pr *httputil.ProxyRequest) {
	upstream := pr.In.Context().Value(UpStreamURL).(*url.URL)
	for _, key := range slices.Collect(maps.Keys(pr.Out.Header)) {
		if strings.HasPrefix(strings.ToLower(key), "x-cache-") {
			pr.Out.Header.Del(key)
		}
	}
	pr.SetURL(upstream)
	pr.Out.Host = upstream.Host
	pr.Out.Header.Set("Host", upstream.Host)
}

var (
	defaultProxy    *httputil.ReverseProxy
	noRedirectProxy *httputil.ReverseProxy
)

func InitProxy() {
	defaultProxy = &httputil.ReverseProxy{
		Rewrite:   proxyRewrite,
		Transport: newHTTPRoundTripper(true),
	}
	noRedirectProxy = &httputil.ReverseProxy{
		Rewrite:   proxyRewrite,
		Transport: newHTTPRoundTripper(false),
	}
}

type Server struct {
	getUrl       func(*http.Request) (*url.URL, error)
	cacheKeyTmpl *template.Template
	logger       *slog.Logger
	ttlMap       map[int]time.Duration
	proxy        *httputil.ReverseProxy
	*ServerOpt
}

func NewServer(opt ServerOpt) (obj *Server, err error) {
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

	if opt.NoRedirect {
		s.proxy = noRedirectProxy
	} else {
		s.proxy = defaultProxy
	}
	obj = s
	return
}

/*
HandlePrune 处理清理缓存请求

 1. 根据请求路径查找缓存
 2. 根据缓存索引键清理缓存
 3. 流式返回清理的缓存键
*/
func (s *Server) HandlePrune(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, "[")
	defer fmt.Fprint(w, "]")
	enc := json.NewEncoder(w)
	deleted := false
	for key := range s.Cache.Scan(ctx, s.Vserver.Host, r.URL.Path) {
		logger.Debug("查询到键", "key", key)
		if deleted {
			fmt.Fprint(w, ",")
		}
		s.Cache.DeleteByKey(ctx, key)
		enc.Encode(strings.TrimPrefix(key, CacheIndexPrefix))
		deleted = true
	}
}

/*
executeTemplate 渲染缓存键
*/
func (s *Server) executeTemplate(data TemplateOpt) (resp string, err error) {
	var buf bytes.Buffer
	if err = s.cacheKeyTmpl.Execute(&buf, data); err != nil {
		return
	}
	resp = buf.String()
	return
}

/*
handleCache 渲染缓存键并获取缓存

 1. 交由 executeTemplate 获取缓存键
 2. 获取缓存并直接返回
*/
func (s *Server) handleCache(w http.ResponseWriter, r *http.Request) (cacheKey string, resp *CachedResp) {
	upstream, ok := r.Context().Value(UpStreamURL).(*url.URL)
	if !ok {
		s.logger.Warn("没有传入上游URL")
		http.Error(w, http.StatusText(http.StatusBadGateway), http.StatusBadGateway)
		return
	}
	cacheKey, err := s.executeTemplate(TemplateOpt{
		Upstream: upstream,
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

/*
getTTL 按序获取数据缓存时间

 1. 从请求头 X-Cache-TTL-{statusCode} 获取duration
 2. 从请求头 X-Cache-TTL-{2,3,4,5}XX 获取duration
 3. 从请求头 X-Cache-TTL 获取duration
 4. 从配置文件中获取duraion
*/
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

/*
handleCacheData 处理数据缓存

 1. 使用 recover 判断数据传输是否完成
 2. 如果数据传输完成，将响应体储存到缓存
 3. 如果数据传输未完成，但缓存的响应体数据量和数据长度一致，依旧将响应体储存到缓存
 4. 如果数据传输未完成，不进行缓存
*/
func (s *Server) handleCacheData(url, upstream *url.URL, cacheKey string, pw *CacheResponseWriter, resp *CachedResp) {
	err := recover()
	switch err {
	case nil:
		s.logger.Debug("数据处理完成", "statusCode", resp.Code)
		if pw.cached && pw.buffer.Len() > 0 {
			resp.Data = pw.buffer.Bytes()
			s.Cache.SetCache(context.TODO(), s.Vserver.Host, upstream.Path, cacheKey, resp, pw.ttl)
		}
	case http.ErrAbortHandler:
		contentLength := int(pw.ContentLength())
		if pw.cached && pw.buffer.Len() > 0 && contentLength > 0 && pw.buffer.Len() == contentLength {
			resp.Data = pw.buffer.Bytes()
			s.Cache.SetCache(context.TODO(), s.Vserver.Host, upstream.Path, cacheKey, resp, pw.ttl)
		} else {
			s.logger.Info("转发数据结束，数据不完整或未验证", "err", err, "path", url.Path, "contentLength", contentLength, "transform", pw.buffer.Len())
		}
	default:
		s.logger.Info("转发数据失败", "err", err, "path", url.Path)
	}
}

/*
HandleProxy 处理上游请求响应

 1. 使用 CacheResponseWriter 代理 http.ResponseWriter，捕获状态码与响应体
 2. 使用 CachedResp 储存响应
 3. 交由 handleCacheData 在完成时处理缓存
*/
func (s *Server) HandleProxy(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		upstream := ctx.Value(UpStreamURL).(*url.URL)
		cacheKey := ctx.Value(CacheKey).(string)
		resp := CachedResp{}
		pw := CacheResponseWriter{
			w:      w,
			resp:   &resp,
			getTTL: s.getTTL(r.Header),
			cached: true,
		}
		defer s.handleCacheData(r.URL, upstream, cacheKey, &pw, &resp)
		next.ServeHTTP(&pw, r)
	})
}

/*
GetCache 判断是否存在缓存，并决定是否转发到上游

 1. 交由 handleCache 渲染缓存键并获取缓存
 2. 如果有缓存，直接返回缓存的数据
 3. 否则将缓存键设置到上下文，继续执行
*/
func (s *Server) GetCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, cached := s.handleCache(w, r)
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

/*
GetUpstream 渲染上游地址

 1. 使用请求体数据渲染上游地址，并设置到上下文
 2. 继续执行
*/
func (s *Server) GetUpstream(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstream, err := s.getUrl(r)
		s.logger.Debug("获取上游URL", "upstream", upstream)
		if err != nil {
			s.logger.Error("获取上游URL失败", "err", err)
			http.Error(w, http.StatusText(http.StatusBadGateway), http.StatusBadGateway)
			return
		}
		ctx := context.WithValue(r.Context(), UpStreamURL, upstream)
		r = r.WithContext(ctx)
		next.ServeHTTP(w, r)
	})
}

/*
HandleRoute 创建路由
*/
func (s *Server) HandleRoute(pr, cr *mux.Route, host string) (error, error) {
	pr.Methods(http.MethodGet, http.MethodHead).Handler(s.GetUpstream(s.GetCache(s.HandleProxy(s.proxy))))
	cr.Methods("PRUNE").HandlerFunc(s.HandlePrune)
	if len(host) > 0 {
		pr.Host(host)
		cr.Host(host)
	}
	return pr.GetError(), cr.GetError()
}
