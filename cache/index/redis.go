package index

import (
	"context"
	"fmt"
	"iter"
	"log/slog"
	"slices"
	"strings"

	"github.com/boringcat/cat-proxy-cache/cache"
	"github.com/boringcat/cat-proxy-cache/config"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/rueidis"
)

const (
	RedisPrefix = "cat-cache-index:"
)

type RedisIndex struct {
	backend      rueidis.Client
	itemsPerScan int64
	logger       *slog.Logger
}

func NewRedisIndex(conf *config.RedisConfig) (*RedisIndex, error) {
	client, err := conf.GetClient(true)
	if err != nil {
		return nil, errors.Wrap(err, "创建Redis客户端失败")
	}
	n, err := client.Do(context.TODO(), client.B().Ping().Build()).ToString()
	if err != nil {
		return nil, errors.Wrap(err, "连接Redis失败")
	} else if n != "PONG" {
		return nil, cache.ErrRedisPingFailed
	}
	var itemsPerScan int64 = 64
	if conf.ItemsPerScan > 0 {
		itemsPerScan = conf.ItemsPerScan
	}
	return &RedisIndex{
		backend:      client,
		itemsPerScan: itemsPerScan,
		logger:       slog.With("logger", "redis-index"),
	}, nil
}

func (i *RedisIndex) Index(host, path string) (key string) {
	return fmt.Sprint(RedisPrefix, host, "/", strings.TrimPrefix(path, "/"))
}

func (i *RedisIndex) Get(ctx context.Context, key string) (keys []string, err error) {
	scanner := rueidis.NewScanner(func(cursor uint64) (rueidis.ScanEntry, error) {
		timer := prometheus.NewTimer(prune_scan_histogram.WithLabelValues("SSCAN"))
		defer timer.ObserveDuration()
		return i.backend.Do(
			ctx,
			i.backend.B().Sscan().
				Key(key).
				Cursor(cursor).
				Count(i.itemsPerScan).
				Build(),
		).AsScanEntry()
	})
	return slices.Collect(scanner.Iter()), scanner.Err()
}

func (i *RedisIndex) Set(ctx context.Context, key string, keys []string) (count int64, err error) {
	del := i.backend.B().Del().Key(key).Build()
	sadd := i.backend.B().Sadd().Key(key).Member(keys...).Build()
	resp := i.backend.DoMulti(ctx, del, sadd)
	return resp[1].AsInt64()
}

func (i *RedisIndex) Del(ctx context.Context, keys ...string) (ok bool, err error) {
	n, err := i.backend.Do(ctx, i.backend.B().Del().Key(keys...).Build()).AsInt64()
	if err == rueidis.Nil {
		err = nil
	}
	ok = n == 1
	return
}

func (i *RedisIndex) Append(ctx context.Context, key string, keys ...string) (count int64, err error) {
	count, err = i.backend.Do(ctx, i.backend.B().Sadd().Key(key).Member(keys...).Build()).AsInt64()
	return
}

func (i *RedisIndex) Remove(ctx context.Context, key string, keys ...string) (count int64, err error) {
	count, err = i.backend.Do(ctx, i.backend.B().Srem().Key(key).Member(keys...).Build()).AsInt64()
	return
}

func (i *RedisIndex) Keys(ctx context.Context, pattern string) iter.Seq[string] {
	scanner := rueidis.NewScanner(func(cursor uint64) (rueidis.ScanEntry, error) {
		timer := prometheus.NewTimer(prune_scan_histogram.WithLabelValues("SCAN"))
		defer timer.ObserveDuration()
		return i.backend.Do(
			ctx,
			i.backend.B().Scan().
				Cursor(cursor).
				Match(pattern).
				Count(i.itemsPerScan).
				Build(),
		).AsScanEntry()
	})
	return scanner.Iter()
}

func (i *RedisIndex) Items(ctx context.Context, pattern string) iter.Seq2[string, []string] {
	return func(yield func(string, []string) bool) {
		for key := range i.Keys(ctx, pattern) {
			keys, err := i.Get(ctx, key)
			if err != nil {
				i.logger.Warn("Items获取Key异常", "key", key, "err", err)
			}
			if keys != nil {
				if !yield(key, keys) {
					return
				}
			}
		}
	}
}
