package cache

import (
	"context"
	"iter"
	"net/http"
	"net/url"
	"time"

	"github.com/pkg/errors"
)

var (
	ErrFlushNotSupport = errors.New("不支持刷新缓存过期时间")
	ErrRedisPingFailed = errors.New("ping Redis失败")
)

type CacheData struct {
	Code   int
	Header http.Header
	Data   []byte
}

type TemplateOpt struct {
	Upstream *url.URL
	Request  *http.Request
}

type IndexCacher interface {
	// 获取索引键
	Index(host, path string) (key string)
	// 获取索引
	Get(ctx context.Context, key string) (keys []string, err error)
	// 覆写索引
	Set(ctx context.Context, key string, keys []string) (count int64, err error)
	// 删除索引
	Del(ctx context.Context, keys ...string) (ok bool, err error)
	// 向索引中添加key
	Append(ctx context.Context, key string, keys ...string) (count int64, err error)
	// 从索引中移除key
	Remove(ctx context.Context, key string, keys ...string) (count int64, err error)
	// 扫描索引
	Keys(ctx context.Context, pattern string) iter.Seq[string]
	// 扫描索引并获取所有Key
	Items(ctx context.Context, pattern string) iter.Seq2[string, []string]
}

type BlobCacher interface {
	// 获取缓存键
	Index(ctx context.Context, opt *TemplateOpt) (key string, err error)
	// 获取缓存数据
	Get(ctx context.Context, key string) (data *CacheData, err error)
	// 设置缓存数据
	Set(ctx context.Context, key string, data *CacheData, ttl time.Duration) (ok bool, err error)
	// 输出缓存数据
	Del(ctx context.Context, keys ...string) (ok bool, err error)
	// 刷新数据缓存时间
	Flush(ctx context.Context, key string, ttl time.Duration) (ok bool, err error)
}
