package main

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"
	"iter"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

type CachedResp struct {
	Code   int
	Header http.Header
	Data   []byte
}

type Cacher struct {
	client      redis.Cmdable
	getCacheKey func(string) string
	logger      *slog.Logger
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
	return &Cacher{
		client:      client,
		getCacheKey: getCacheKey,
		logger:      logger.With("logger", "cache"),
	}
}

func (c *Cacher) getIndexKey(host, path string) string {
	if strings.HasPrefix(path, "/") {
		return fmt.Sprintf("cache-index:%s%s", host, path)
	} else {
		return fmt.Sprintf("cache-index:%s/%s", host, path)
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
	if result, err := c.client.RPush(ctx, c.getIndexKey(host, path), key).Result(); err != nil {
		c.logger.Info("设置缓存索引失败", "err", err, "key", key, "result", result)
	} else {
		c.logger.Info("设置缓存索引成功", "key", key, "result", result)
	}
}

func (c *Cacher) Keys(ctx context.Context, host string) iter.Seq[string] {
	return func(yield func(string) bool) {
		resp := c.client.Scan(ctx, 0, c.getIndexKey(host, "*"), 1024)
		if err := resp.Err(); err != nil {
			c.logger.Warn("Scan Keys失败", "err", err, "host", host)
		}
		iter := resp.Iterator()
		for iter.Next(ctx) {
			if !yield(iter.Val()) {
				return
			}
		}
	}

}
func (c *Cacher) Values(ctx context.Context, host string) iter.Seq[[]string] {
	return func(yield func([]string) bool) {
		for _, vs := range c.Items(ctx, host) {
			if !yield(vs) {
				return
			}
		}
	}
}
func (c *Cacher) Items(ctx context.Context, host string) iter.Seq2[string, []string] {
	return func(yield func(string, []string) bool) {
		resp := c.client.Scan(ctx, 0, c.getIndexKey(host, "*"), 1024)
		if err := resp.Err(); err != nil {
			c.logger.Warn("Scan Keys失败", "err", err, "host", host)
		}
		iter := resp.Iterator()
		for iter.Next(ctx) {
			key := iter.Val()
			vals, err := c.client.LRange(ctx, key, 0, -1).Result()
			switch err {
			case nil:
				if !yield(key, vals) {
					return
				}
			case redis.Nil:
				continue
			default:
				c.logger.Warn("获取所有Keys失败", "err", err, "host", host, "path", key)
				continue
			}
		}
	}
}
func (c *Cacher) DeleteByPath(ctx context.Context, host, path string) {
	key := c.getIndexKey(host, path)
	keys, err := c.client.LRange(ctx, key, 0, -1).Result()
	switch err {
	case nil:
	case redis.Nil:
		return
	default:
		c.logger.Warn("获取缓存键失败", "err", err, "host", host, "path", path)
		return
	}
	if err := c.client.Del(ctx, append(keys, key)...).Err(); err != nil {
		c.logger.Warn("删除缓存失败", "err", err, "host", host, "path", path)
		return
	}
}
