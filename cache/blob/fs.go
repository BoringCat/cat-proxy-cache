package blob

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/gob"
	"encoding/hex"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/boringcat/cat-proxy-cache/cache"
	"github.com/boringcat/cat-proxy-cache/config"
	"github.com/pkg/errors"
)

var (
	ErrPathIsNotDir  = errors.New("目标路径不是一个目录")
	ErrEmptyHashType = errors.New("没有设置路径哈希方法")
)

type FsBlob struct {
	dir          string
	hash         func(r io.WriterTo) string
	cacheKeyTmpl *template.Template
	logger       *slog.Logger
}

func NewFsBlob(conf *config.FsConfig, opt *config.BlobOption) (*FsBlob, error) {
	info, err := os.Stat(conf.Dir)
	if err != nil {
		return nil, errors.Wrap(err, "检查文件夹失败")
	}
	if info.Mode().Type() != fs.ModeDir {
		return nil, errors.Wrap(ErrPathIsNotDir, "检查文件夹失败")
	}
	fd, err := os.Create(filepath.Join(conf.Dir, ".test-perm"))
	if err != nil {
		return nil, errors.Wrap(err, "检查文件夹权限失败")
	}
	fd.Close()

	var getLevel func(string) string

	if strings.Contains(conf.LevelKey, ":") {
		levels := strings.SplitN(conf.LevelKey, ":", 2)
		l1, _ := strconv.Atoi(levels[0])
		l2, _ := strconv.Atoi(levels[1])
		if l1 > 0 && l2 > 0 {
			getLevel = func(s string) string {
				s1 := s[len(s)-l1*2 : len(s)]
				_s := s[:len(s)-l1*2]
				s2 := _s[len(s)-l2*2 : len(s)]
				return filepath.Join(s1, s2, s)
			}
		}
	} else if l1, err := strconv.Atoi(conf.LevelKey); err == nil {
		getLevel = func(s string) string {
			s1 := s[len(s)-l1*2 : len(s)]
			return filepath.Join(s1, s)
		}
	} else {
		getLevel = func(s string) string { return s }
	}

	var hfunc func(r io.WriterTo) string

	switch strings.ToLower(conf.Hash) {
	case "sha1":
		hfunc = func(r io.WriterTo) string {
			w := sha1.New()
			r.WriteTo(w)
			return getLevel(hex.EncodeToString(w.Sum(nil)))
		}
	case "sha256":
		hfunc = func(r io.WriterTo) string {
			w := sha256.New()
			r.WriteTo(w)
			return getLevel(hex.EncodeToString(w.Sum(nil)))
		}
	case "sha512":
		hfunc = func(r io.WriterTo) string {
			w := sha512.New()
			r.WriteTo(w)
			return getLevel(hex.EncodeToString(w.Sum(nil)))
		}
	default:
		return nil, ErrEmptyHashType
	}

	if len(opt.CacheKey) == 0 {
		return nil, ErrEmptyCacheKey
	}
	var cacheKeyTmpl *template.Template
	if cacheKeyTmpl, err = template.New(conf.Dir).Parse(opt.CacheKey); err != nil {
		return nil, errors.Wrap(err, "创建缓存键模板失败")
	}

	return &FsBlob{
		dir:          conf.Dir,
		hash:         hfunc,
		cacheKeyTmpl: cacheKeyTmpl,
		logger:       slog.With("logger", "fs-blob"),
	}, nil
}

func (b *FsBlob) Index(ctx context.Context, opt *cache.TemplateOpt) (key string, err error) {
	var buf bytes.Buffer
	if err = b.cacheKeyTmpl.Execute(&buf, opt); err != nil {
		return
	}
	key = b.hash(&buf)
	return
}

func (b *FsBlob) getDir(key string) string {
	return filepath.Join(b.dir, key)
}

func (b *FsBlob) Get(ctx context.Context, key string) (data *cache.CacheData, err error) {
	fd, err := os.Open(b.getDir(key))
	if err != nil {
		b.logger.Info("读取缓存失败", "err", err, "key", key)
		return
	}
	defer fd.Close()
	data = new(cache.CacheData)
	if err = gob.NewDecoder(fd).Decode(data); err != nil {
		b.logger.Info("解析缓存失败", "err", err, "key", key)
		return
	}
	b.logger.Debug("读取缓存成功", "key", key)
	return
}

func (b *FsBlob) Set(ctx context.Context, key string, data *cache.CacheData, ttl time.Duration) (ok bool, err error) {
	fd, err := os.Create(b.getDir(key))
	if err != nil {
		b.logger.Info("设置缓存失败", "err", err, "key", key)
		return
	}
	defer fd.Close()
	enc := gob.NewEncoder(fd)
	if err = enc.Encode(data); err != nil {
		b.logger.Info("编码缓存数据失败", "err", err, "key", key)
		return
	}
	return ok, nil
}

func (b *FsBlob) Del(ctx context.Context, keys ...string) (ok bool, err error) {
	for _, key := range keys {
		if err = os.Remove(b.getDir(key)); err != nil {
			b.logger.Info("删除缓存数据失败", "err", err, "key", key)
		}
	}
	ok = true
	return
}

func (b *FsBlob) Flush(ctx context.Context, key string, ttl time.Duration) (ok bool, err error) {
	return false, cache.ErrFlushNotSupport
}
