package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"text/template"
	"time"
)

type contentType int

const (
	B   = 1
	KiB = 1 << 10
	MiB = 1 << 20
	GiB = 1 << 30

	respStatusCode contentType = iota
)

type cacheOpt struct {
	ctx             context.Context
	r               io.ReadCloser
	host, path, key string
	cacher          *Cacher
	size            int64
	code            int
	header          http.Header
	ttl             time.Duration
}

func copyRequest(r *http.Request, url string) (req *http.Request, err error) {
	req, err = http.NewRequestWithContext(r.Context(), r.Method, url, r.Body)
	if err != nil {
		return
	}
	for k, vs := range r.Header {
		lk := strings.ToLower(k)
		switch lk {
		case "x-cache-ttl", "host", "x-real-ip", "x-forwarded-for", "x-forwarded-proto", "upgrade":
			continue
		}
		if strings.HasPrefix(lk, "x-cache-ttl-") {
			continue
		}
		req.Header[k] = make([]string, len(vs))
		copy(req.Header[k], vs)
	}
	req.Header.Del("Host")
	req.Header.Del("X-Real-IP")
	req.Header.Del("X-Forwarded-For")
	req.Header.Del("X-Forwarded-Proto")
	req.Header.Del("Upgrade")
	req.Header.Set("Connection", "keep-alive")
	logger.Debug("构建上游请求", "url", req.URL, "method", req.Method, "headers", req.Header)
	return
}

func executeTemplate(tpl *template.Template, data any) (resp string, err error) {
	var buf bytes.Buffer
	if err = tpl.Execute(&buf, data); err != nil {
		return
	}
	resp = buf.String()
	return
}

func setCached(opt *cacheOpt) {
	defer opt.r.Close()
	var err error
	cache := CachedResp{Code: opt.code, Header: opt.header}
	cache.Data, err = io.ReadAll(opt.r)
	if err != nil {
		logger.Debug("读取缓存数据失败", "err", err)
		return
	} else if err = context.Cause(opt.ctx); err != nil && err != context.Canceled {
		// EOF情况之一: 客户端取消下载
		logger.Debug("验证缓存数据失败", "err", err)
		return
	} else if length := int64(len(cache.Data)); opt.size > 0 && length != opt.size {
		// EOF情况之一: 数据长度不匹配
		logger.Debug("验证缓存数据失败", "size", opt.size, "length", length)
		return
	}
	opt.cacher.SetCache(opt.host, opt.path, opt.key, &cache, opt.ttl)
}

func NewServer(vs *VServer, p *Path, client *http.Client, cache *Cacher) (handleContent, handlePrune http.HandlerFunc, err error) {
	var getUrl func(*http.Request) string
	var cacheKeyTmpl *template.Template
	logger := logger.With("logger", "server", "host", vs.Host, "prefix", p.Prefix)
	ttlMap := map[int]time.Duration{}
	if vs.TTL != nil {
		copyMap(ttlMap, *vs.TTL)
	}
	if p.TTL != nil {
		copyMap(ttlMap, *p.TTL)
	}
	cacheKey := orderValue(p.CacheKey, vs.CacheKey)
	if cacheKey == nil {
		err = fmt.Errorf("没有配置缓存键模板")
		return
	}
	upstream := orderValue(p.Upstream, vs.Upstream)
	if upstream == nil {
		err = fmt.Errorf("没有配置上游地址")
		return
	}
	if cacheKeyTmpl, err = template.New(fmt.Sprint(vs.Host, p.Prefix)).Parse(*cacheKey); err != nil {
		return
	}
	if len(p.Expr) > 0 {
		var expr *regexp.Regexp
		if expr, err = regexp.Compile(p.Expr); err != nil {
			return
		}
		repl := p.Repl
		if len(repl) == 0 {
			repl = "$1"
		}
		logger.Debug("构建表达式", "expr", expr, "repl", repl)
		getUrl = func(r *http.Request) string {
			return fmt.Sprint(*upstream, expr.ReplaceAllString(r.URL.String(), repl))
		}
	} else {
		getUrl = func(r *http.Request) string {
			return fmt.Sprint(*upstream, r.URL)
		}
	}
	handlePrune = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, "[")
		defer fmt.Fprint(w, "]")
		enc := json.NewEncoder(w)
		deleted := false
		for key := range cache.Keys(vs.Host) {
			if strings.HasSuffix(r.URL.Path, "*") {
				prefix := strings.TrimSuffix(r.URL.Path, "*")
				if strings.HasPrefix(key, prefix) {
					if deleted {
						fmt.Fprint(w, ",")
					}
					cache.DeleteByPath(vs.Host, key)
					enc.Encode(key)
					deleted = true
				}
			} else if key == r.URL.Path {
				if deleted {
					fmt.Fprint(w, ",")
				}
				cache.DeleteByPath(vs.Host, key)
				enc.Encode(key)
				deleted = true
			}
		}
		if deleted {
			cache.Flush()
		}
		w.Header().Set("Content-Type", "application/json")
	}
	handleContent = func(w http.ResponseWriter, r *http.Request) {
		upstream := getUrl(r)
		logger.Debug("获取上游URL", "upstream", upstream)
		req, err := copyRequest(r, upstream)
		if err != nil {
			logger.Info("创建上游请求失败", "err", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		defer req.Body.Close()
		cacheKey, err := executeTemplate(cacheKeyTmpl, req)
		if err != nil {
			logger.Warn("获取缓存键失败", "err", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		if cached := cache.GetCache(cacheKey); cached != nil {
			for k, vs := range cached.Header {
				for _, v := range vs {
					w.Header().Add(k, v)
				}
			}
			w.Header().Set("X-Cache-Status", "HIT")
			w.WriteHeader(cached.Code)
			w.Write(cached.Data)
			return
		}
		resp, err := client.Do(req)
		if err != nil {
			logger.Warn("转发请求到上游失败", "err", err)
			http.Error(w, http.StatusText(http.StatusBadGateway), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		ttl, err := getTTLFromHeader(r, resp)
		logger.Debug("获取到TTL", "ttl", ttl, "status_code", resp.StatusCode)
		if err != nil {
			var ok bool
			if ttl, ok = ttlMap[resp.StatusCode]; !ok {
				ttl = ttlMap[0]
			}
		}
		logger.Debug("获取到TTL", "ttl", ttl, "status_code", resp.StatusCode)
		var mw io.Writer = w
		context, cancel := context.WithCancelCause(context.Background())
		defer cancel(nil)
		if ttl >= time.Second {
			pr, pw := io.Pipe()
			mw = io.MultiWriter(pw, w)
			defer pw.Close()
			datalen := resp.ContentLength
			if req.Method == http.MethodHead || req.Method == http.MethodOptions {
				datalen = -1
			}
			go setCached(&cacheOpt{
				context, pr, vs.Host, r.URL.Path, cacheKey, cache,
				datalen, resp.StatusCode, resp.Header, ttl,
			})
			w.Header().Set("X-Cache-Status", "MISS")
		} else {
			w.Header().Set("X-Cache-Status", "BYPASS")
		}
		copyHTTPHeader(w, resp.Header)
		w.WriteHeader(resp.StatusCode)
		if _, err = io.CopyBuffer(mw, resp.Body, make([]byte, 1*MiB)); err != nil {
			cancel(io.ErrUnexpectedEOF)
			logger.Info("转发数据失败", "err", err, "path", r.URL.Path)
		}
	}
	return
}
