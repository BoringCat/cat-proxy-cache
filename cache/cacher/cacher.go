package cacher

import (
	"context"
	"iter"
	"log/slog"
	"strings"
	"time"

	"github.com/pkg/errors"

	structs "github.com/boringcat/cat-proxy-cache/cache"
	blob_cache "github.com/boringcat/cat-proxy-cache/cache/blob"
	index_cache "github.com/boringcat/cat-proxy-cache/cache/index"
	"github.com/boringcat/cat-proxy-cache/config"
)

var (
	ErrCacheBlobIsNil = errors.New("cache.blob必须配置")
)

func emptyIter[T any](yield func(T) bool) {}

// func emptyIter2[K, V any](yield func(K, V) bool) {}

type Cacher struct {
	index  structs.IndexCacher
	blob   structs.BlobCacher
	logger *slog.Logger
}

func (c *Cacher) HasIndex() bool { return c.index != nil }

func (c *Cacher) CacheKey(ctx context.Context, opt structs.TemplateOpt) (string, error) {
	return c.blob.Index(ctx, &opt)
}

func (c *Cacher) Get(ctx context.Context, key string) *structs.CacheData {
	data, err := c.blob.Get(ctx, key)
	if err != nil {
		c.logger.Info("读取缓存失败", "err", err, "key", key)
		return nil
	}
	return data
}

func (c *Cacher) Set(
	ctx context.Context, host, path, key string,
	data *structs.CacheData, ttl time.Duration,
) {
	if ok, err := c.blob.Set(ctx, key, data, ttl); err != nil {
		c.logger.Info("设置缓存失败", "err", err, "host", host, "path", path, "key", key)
		return
	} else if !ok {
		c.logger.Info("设置缓存失败", "host", host, "path", path, "key", key)
		return
	}
	if c.index == nil {
		return
	}
	if n, err := c.index.Append(ctx, c.index.Index(host, path), key); err != nil {
		c.logger.Info("设置缓存索引失败", "err", err, "host", host, "path", path, "key", key)
	} else {
		c.logger.Debug("设置缓存索引成功", "count", n, "host", host, "path", path, "key", key)
	}
}

func (c *Cacher) Scan(ctx context.Context, host, pattern string) iter.Seq[string] {
	if c.index == nil {
		return emptyIter
	}
	return c.index.Keys(ctx, c.index.Index(host, pattern))
}

func (c *Cacher) DeleteByScan(ctx context.Context, host, pattern string) iter.Seq[string] {
	if c.index == nil {
		return emptyIter
	}
	return func(yield func(string) bool) {
		for idx, keys := range c.index.Items(ctx, c.index.Index(host, pattern)) {
			if _, err := c.blob.Del(ctx, keys...); err == nil {
				c.index.Del(ctx, idx)
			}
			if !yield(strings.TrimPrefix(idx, index_cache.RedisPrefix)) {
				return
			}
		}
	}
}

func NewCacher(conf *config.CacheConfig) (c *Cacher, err error) {
	var index structs.IndexCacher
	var blob structs.BlobCacher
	if conf.Blob == nil {
		return nil, ErrCacheBlobIsNil
	} else {
		switch conf.Blob.Backend {
		case "redis":
			if blob, err = blob_cache.NewRedisBlob(conf.Blob.Redis, &conf.Blob.BlobOption); err != nil {
				err = errors.Wrap(err, "创建缓存失败")
				return
			}
			// case "memcached":
			// 	if blob, err = blob_cache.NewMemcacheBlob(conf.Blob.Memcached); err != nil {
			// 		return
			// 	}
		}
	}
	if conf.Index != nil {
		switch conf.Index.Backend {
		case "redis":
			if index, err = index_cache.NewRedisIndex(conf.Index.Redis); err != nil {
				err = errors.Wrap(err, "创建索引失败")
				return
			}
		}
	}
	c = &Cacher{index: index, blob: blob, logger: slog.With("logger", "cacher")}
	return
}
