package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"time"

	"github.com/boringcat/cat-proxy-cache/cache"
	"github.com/boringcat/cat-proxy-cache/cache/cacher"
	"github.com/boringcat/cat-proxy-cache/config"
	"github.com/boringcat/cat-proxy-cache/utils"
	"github.com/gorilla/mux"
)

type ServerOpt struct {
	Vserver     *config.VServer
	Path        *config.Path
	Cache       *cacher.Cacher
	MaxRedirect *int
}

func (opt *ServerOpt) getModel() *config.Model {
	return &config.Model{
		Upstream:    utils.OrderValue(opt.Path.Upstream, opt.Vserver.Upstream),
		TTL:         utils.OrderValue(opt.Path.TTL, opt.Vserver.TTL),
		MaxRedirect: utils.OrderValue(opt.Path.MaxRedirect, opt.Vserver.MaxRedirect, opt.MaxRedirect),
	}
}

type Server struct {
	getUrl func(*http.Request) (*url.URL, error)
	logger *slog.Logger
	ttlMap map[int]time.Duration
	proxy  *httputil.ReverseProxy
	*ServerOpt
}

func NewServer(opt ServerOpt) (obj *Server, err error) {
	model := opt.getModel()
	s := Server{
		logger:    slog.With("logger", "server", "host", opt.Vserver.Host, "prefix", opt.Path.Prefix),
		ttlMap:    map[int]time.Duration{},
		ServerOpt: &opt,
	}
	if opt.Vserver.TTL != nil {
		utils.CopyMap(s.ttlMap, *opt.Vserver.TTL)
	}
	if opt.Path.TTL != nil {
		utils.CopyMap(s.ttlMap, *opt.Path.TTL)
	}
	if model.Upstream == nil {
		err = fmt.Errorf("没有配置上游地址")
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
		s.logger.Debug("构建表达式", "expr", expr, "repl", repl)
		s.getUrl = func(r *http.Request) (*url.URL, error) {
			return url.Parse(fmt.Sprint(*model.Upstream, expr.ReplaceAllString(r.URL.String(), repl)))
		}
	} else {
		s.getUrl = func(r *http.Request) (*url.URL, error) {
			return url.Parse(fmt.Sprint(*model.Upstream, r.URL))
		}
	}

	maxRedirect := 10
	if opt.MaxRedirect != nil {
		maxRedirect = *opt.MaxRedirect
	}
	s.proxy = getProxy(maxRedirect)
	obj = &s
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
	for key := range s.Cache.DeleteByScan(ctx, s.Vserver.Host, r.URL.Path) {
		s.logger.Debug("清理了键", "key", key)
		if deleted {
			fmt.Fprint(w, ",")
		}
		enc.Encode(key)
		deleted = true
	}
}

/*
handleCache 渲染缓存键并获取缓存

 1. 交由 executeTemplate 获取缓存键
 2. 获取缓存并直接返回
*/
func (s *Server) handleCache(w http.ResponseWriter, r *http.Request) (cacheKey string, resp *cache.CacheData) {
	upstream, ok := getUpStream(r.Context())
	if !ok {
		s.logger.Warn("没有传入上游URL")
		http.Error(w, http.StatusText(http.StatusBadGateway), http.StatusBadGateway)
		return
	}
	cacheKey, err := s.Cache.CacheKey(r.Context(), cache.TemplateOpt{
		Upstream: upstream,
		Request:  r,
	})
	if err != nil {
		s.logger.Warn("获取缓存键失败", "err", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	s.logger.Debug("获取缓存键", "key", cacheKey)
	resp = s.Cache.Get(r.Context(), cacheKey)
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
func (s *Server) handleCacheData(url, upstream *url.URL, cacheKey string, pw *CacheResponseWriter) {
	err := recover()
	switch err {
	case nil:
		s.logger.Debug("数据处理完成", "statusCode", pw.statusCode)
		if pw.cached && pw.buffer.Len() > 0 {
			resp := pw.makeData()
			s.Cache.Set(context.TODO(), s.Vserver.Host, upstream.Path, cacheKey, resp, pw.ttl)
		}
	case http.ErrAbortHandler:
		contentLength := int(pw.ContentLength())
		if pw.cached && pw.buffer.Len() > 0 && contentLength > 0 && pw.buffer.Len() == contentLength {
			resp := pw.makeData()
			s.Cache.Set(context.TODO(), s.Vserver.Host, upstream.Path, cacheKey, resp, pw.ttl)
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
		upstream, ok := getUpStream(ctx)
		if !ok {
			s.logger.Warn("没有传入上游URL")
			next.ServeHTTP(w, r)
			return
		}
		cacheKey, ok := getCacheKey(ctx)
		if !ok {
			s.logger.Warn("没有传入缓存键")
			next.ServeHTTP(w, r)
			return
		}
		pw := CacheResponseWriter{
			w:      w,
			getTTL: s.getTTL(r.Header),
			cached: true,
		}
		defer s.handleCacheData(r.URL, upstream, cacheKey, &pw)
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
			r = r.WithContext(setCacheKey(r.Context(), key))
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
		r = r.WithContext(setUpStream(r.Context(), upstream))
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
