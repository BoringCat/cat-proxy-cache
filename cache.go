package main

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"
	"iter"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
)

const (
	CacheIndexPrefix = "cache-index:"
)

type CachedResp struct {
	Code   int
	Header http.Header
	Data   []byte
}

type Cacher struct {
	client       redis.Cmdable
	getCacheKey  func(string) string
	logger       *slog.Logger
	itemsPerScan int64
}

func NewCache(conf *Rediscached) *Cacher {
	var getCacheKey func(string) string
	var client redis.Cmdable
	if conf.IsCluster {
		client = redis.NewClusterClient(&redis.ClusterOptions{
			Addrs:           conf.Address,
			Username:        conf.Username,
			Password:        conf.Password,
			MaxRedirects:    conf.MaxRedirects,
			DialTimeout:     conf.DialTimeout,
			ReadTimeout:     conf.ReadTimeout,
			WriteTimeout:    conf.WriteTimeout,
			PoolSize:        conf.PoolSize,
			PoolTimeout:     conf.PoolTimeout,
			MinIdleConns:    conf.MinIdleConns,
			MaxIdleConns:    conf.MaxIdleConns,
			MaxActiveConns:  conf.MaxActiveConns,
			ConnMaxIdleTime: conf.ConnMaxIdleTime,
			ConnMaxLifetime: conf.ConnMaxLifetime,
			ReadBufferSize:  conf.ReadBufferSize,
			WriteBufferSize: conf.WriteBufferSize,
		})
	} else {
		client = redis.NewClient(&redis.Options{
			Addr:            conf.Address[0],
			DB:              conf.Db,
			Username:        conf.Username,
			DialTimeout:     conf.DialTimeout,
			ReadTimeout:     conf.ReadTimeout,
			WriteTimeout:    conf.WriteTimeout,
			Password:        conf.Password,
			PoolSize:        conf.PoolSize,
			PoolTimeout:     conf.PoolTimeout,
			MinIdleConns:    conf.MinIdleConns,
			MaxIdleConns:    conf.MaxIdleConns,
			MaxActiveConns:  conf.MaxActiveConns,
			ConnMaxIdleTime: conf.ConnMaxIdleTime,
			ConnMaxLifetime: conf.ConnMaxLifetime,
			ReadBufferSize:  conf.ReadBufferSize,
			WriteBufferSize: conf.WriteBufferSize,
		})
	}
	var itemsPerScan = conf.ItemsPerScan
	if itemsPerScan <= 0 {
		itemsPerScan = 10
	}
	return &Cacher{
		client:       client,
		getCacheKey:  getCacheKey,
		logger:       logger.With("logger", "cache"),
		itemsPerScan: itemsPerScan,
	}
}

func (c *Cacher) getIndexKey(host, path string) string {
	if strings.HasPrefix(path, "/") {
		return fmt.Sprint(CacheIndexPrefix, host, path)
	} else {
		return fmt.Sprintf("%s%s/%s", CacheIndexPrefix, host, path)
	}
}

func (c *Cacher) GetCache(ctx context.Context, key string) *CachedResp {
	item, err := c.client.Get(ctx, key).Bytes()
	if err != nil {
		c.logger.Info("读取缓存失败", "err", err, "key", key)
		return nil
	}
	var cached CachedResp
	if err = gob.NewDecoder(bytes.NewBuffer(item)).Decode(&cached); err != nil {
		c.logger.Info("解析缓存失败", "err", err, "key", key)
		return nil
	}
	c.logger.Debug("读取缓存成功", "key", key)
	return &cached
}

func (c *Cacher) SetCache(ctx context.Context, host, path, key string, value *CachedResp, ttl time.Duration) {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(value); err != nil {
		logger.Info("编码缓存数据失败", "err", err)
		return
	}
	if result, err := c.client.Set(ctx, key, buf.Bytes(), ttl).Result(); err != nil {
		c.logger.Info("设置缓存失败", "err", err, "key", key, "result", result)
	} else {
		c.logger.Info("设置缓存成功", "key", key, "result", result)
	}
	if result, err := c.client.SAdd(ctx, c.getIndexKey(host, path), key).Result(); err != nil {
		c.logger.Info("设置缓存索引失败", "err", err, "key", key, "result", result)
	} else {
		c.logger.Info("设置缓存索引成功", "key", key, "result", result)
	}
}

func (c *Cacher) Scan(ctx context.Context, host, path string) iter.Seq[string] {
	return func(yield func(string) bool) {
		timer := prometheus.NewTimer(prune_scan_histogram)
		defer timer.ObserveDuration()
		resp := c.client.Scan(ctx, 0, c.getIndexKey(host, path), c.itemsPerScan)
		if err := resp.Err(); err != nil {
			c.logger.Warn("Scan Keys失败", "err", err, "host", host)
		}
		iterator := resp.Iterator()
		for iterator.Next(ctx) {
			if !yield(iterator.Val()) {
				return
			}
		}
	}
}

func (c *Cacher) sScan(ctx context.Context, key string) iter.Seq[string] {
	return func(yield func(string) bool) {
		resp := c.client.SScan(ctx, key, 0, "*", c.itemsPerScan)
		if err := resp.Err(); err != nil {
			c.logger.Warn("SScan失败", "err", err, "key", key)
		}
		iterator := resp.Iterator()
		for iterator.Next(ctx) {
			if !yield(iterator.Val()) {
				return
			}
		}
	}
}

func (c *Cacher) DeleteByKey(ctx context.Context, key string) {
	keys := slices.Collect(c.sScan(ctx, key))
	c.logger.Debug("获取到缓存键列表", "key", key, "keys", len(keys))
	if err := c.client.Del(ctx, append(keys, key)...).Err(); err != nil {
		c.logger.Warn("删除缓存失败", "err", err, "key", key)
		return
	}
}

func (c *Cacher) DeleteByPath(ctx context.Context, host, path string) {
	key := c.getIndexKey(host, path)
	keys := slices.Collect(c.sScan(ctx, key))
	c.logger.Debug("获取到缓存键列表", "host", host, "path", key, "keys", len(keys))
	if err := c.client.Del(ctx, append(keys, key)...).Err(); err != nil {
		c.logger.Warn("删除缓存失败", "err", err, "host", host, "path", path)
		return
	}
}
