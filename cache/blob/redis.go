package blob

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"
	"log/slog"
	"text/template"
	"time"

	"github.com/boringcat/cat-proxy-cache/cache"
	"github.com/boringcat/cat-proxy-cache/config"
	"github.com/pkg/errors"
	"github.com/redis/rueidis"
)

var (
	ErrEmptyCacheKey = errors.New("缓存键模板为空")
)

type RedisBlob struct {
	backend      rueidis.Client
	cacheKeyTmpl *template.Template
	logger       *slog.Logger
}

func NewRedisBlob(conf *config.RedisConfig, opt *config.BlobOption) (*RedisBlob, error) {
	client, err := conf.GetClient(true)
	if err != nil {
		return nil, errors.Wrap(err, "创建Redis客户端失败")
	}
	ok, err := client.Do(context.TODO(), client.B().Ping().Build()).ToString()
	if err != nil {
		return nil, errors.Wrap(err, "连接Redis失败")
	} else if ok != "PONG" {
		return nil, cache.ErrRedisPingFailed
	}

	if len(opt.CacheKey) == 0 {
		return nil, ErrEmptyCacheKey
	}
	var cacheKeyTmpl *template.Template
	if cacheKeyTmpl, err = template.New(fmt.Sprint(client)).Parse(opt.CacheKey); err != nil {
		return nil, errors.Wrap(err, "创建缓存键模板失败")
	}

	return &RedisBlob{
		backend:      client,
		cacheKeyTmpl: cacheKeyTmpl,
		logger:       slog.With("logger", "redis-blob"),
	}, nil
}

func (b *RedisBlob) Index(ctx context.Context, opt *cache.TemplateOpt) (key string, err error) {
	var buf bytes.Buffer
	if err = b.cacheKeyTmpl.Execute(&buf, opt); err != nil {
		return
	}
	key = buf.String()
	return
}

func (b *RedisBlob) Get(ctx context.Context, key string) (data *cache.CacheData, err error) {
	blob, err := b.backend.Do(ctx, b.backend.B().Get().Key(key).Build()).AsBytes()
	if err != nil && !errors.Is(err, rueidis.Nil) {
		b.logger.Info("读取缓存失败", "err", err, "key", key)
		return
	}
	data = new(cache.CacheData)
	if err = gob.NewDecoder(bytes.NewBuffer(blob)).Decode(data); err != nil {
		b.logger.Info("解析缓存失败", "err", err, "key", key)
		return
	}
	b.logger.Debug("读取缓存成功", "key", key)
	return
}

func (b *RedisBlob) Set(ctx context.Context, key string, data *cache.CacheData, ttl time.Duration) (ok bool, err error) {
	cache_key_length.Observe(float64(len(key)))
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err = enc.Encode(data); err != nil {
		b.logger.Info("编码缓存数据失败", "err", err)
		return
	}
	n, err := b.backend.Do(
		ctx,
		b.backend.B().Set().
			Key(key).
			Value(rueidis.BinaryString(buf.Bytes())).
			Px(ttl).
			Build(),
	).ToString()
	ok = n == "OK"
	return
}

func (b *RedisBlob) Del(ctx context.Context, keys ...string) (ok bool, err error) {
	n, err := b.backend.Do(ctx, b.backend.B().Del().Key(keys...).Build()).AsInt64()
	if err == rueidis.Nil {
		err = nil
	}
	ok = n == 1
	return
}

func (b *RedisBlob) Flush(ctx context.Context, key string, ttl time.Duration) (ok bool, err error) {
	n, err := b.backend.Do(
		ctx,
		b.backend.B().Pexpire().
			Key(key).
			Milliseconds(ttl.Microseconds()).
			Build(),
	).AsInt64()
	if err == rueidis.Nil {
		err = nil
	}
	ok = n == 1
	return
}
