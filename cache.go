package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/gob"
	"encoding/hex"
	"fmt"
	"hash"
	"iter"
	"log/slog"
	"math"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bradfitz/gomemcache/memcache"
	"github.com/miekg/dns"
)

type DnsMode string

const (
	DnsSRV = "dnssrv"
	Dns    = "dns"
)

type DnsServerList struct {
	memcache.ServerList
	client  *dns.Client
	conf    *dns.ClientConfig
	lock    sync.Mutex
	nextq   time.Time
	port    string
	dnsMode DnsMode
	address string
	statics []string
}

func (l *DnsServerList) getOneDnsServer() string {
	return net.JoinHostPort(
		l.conf.Servers[rand.Intn(len(l.conf.Servers))],
		l.conf.Port,
	)
}

func (l *DnsServerList) ResolveServers() {
	switch l.dnsMode {
	case Dns:
		if !l.lock.TryLock() {
			return
		}
		defer l.lock.Unlock()
		var ttl uint32 = math.MaxUint32
		servers := []string{}
		for rr := range doDualTypeQuery(l.client, l.address, l.getOneDnsServer()) {
			ttl = min(ttl, rr.Header().Ttl)
			switch _t := rr.(type) {
			case *dns.A:
				servers = append(servers, net.JoinHostPort(_t.A.String(), l.port))
			case *dns.AAAA:
				servers = append(servers, net.JoinHostPort(_t.AAAA.String(), l.port))
			}
		}
		l.nextq = time.Now().Add(max(time.Second*time.Duration(ttl), 15*time.Second))
		l.ServerList.SetServers(append(l.statics, servers...)...)
	case DnsSRV:
		if !l.lock.TryLock() {
			return
		}
		defer l.lock.Unlock()
		var ttl uint32 = math.MaxUint32
		servers := []string{}
		for rr, port := range doSrvQuery(l.client, l.address, l.getOneDnsServer()) {
			ttl = min(ttl, rr.Header().Ttl)
			switch _t := rr.(type) {
			case *dns.A:
				servers = append(servers, net.JoinHostPort(_t.A.String(), fmt.Sprint(port)))
			case *dns.AAAA:
				servers = append(servers, net.JoinHostPort(_t.AAAA.String(), fmt.Sprint(port)))
			case *dns.SRV:
				servers = append(servers, net.JoinHostPort(_t.Target, fmt.Sprint(_t.Port)))
			}
		}
		l.nextq = time.Now().Add(max(time.Second*time.Duration(ttl), 15*time.Second))
		l.ServerList.SetServers(append(l.statics, servers...)...)
	}
}

func (l *DnsServerList) PickServer(key string) (net.Addr, error) {
	if time.Now().After(l.nextq) {
		go l.ResolveServers()
	}
	return l.ServerList.PickServer(key)
}

func (l *DnsServerList) Each(f func(net.Addr) error) error {
	if time.Now().After(l.nextq) {
		go l.ResolveServers()
	}
	return l.ServerList.Each(f)
}

func NewDnsServerList(s ...string) (*DnsServerList, error) {
	conf, err := dns.ClientConfigFromFile("/etc/resolv.conf")
	if err != nil {
		return nil, err
	}
	sl := DnsServerList{
		client:  new(dns.Client),
		conf:    conf,
		statics: []string{},
	}
	for _, tpl := range s {
		if uri, err := url.Parse(tpl); err != nil {
			sl.statics = append(sl.statics, tpl)
		} else {
			sl.dnsMode = DnsMode(uri.Scheme)
			sl.address = uri.Host
			switch uri.Scheme {
			case Dns:
				sl.port = uri.Port()
				if len(sl.port) == 0 {
					sl.port = "11211"
				}
			case DnsSRV:
			default:
				return nil, fmt.Errorf("不支持的DNS查询模式: %s", uri.Scheme)
			}
		}
	}
	sl.ResolveServers()
	return &sl, nil
}

type CachedResp struct {
	Code   int
	Header http.Header
	Data   []byte
}

type IndexAction string

const (
	IndexAppend IndexAction = "append"
	IndexDelete IndexAction = "delete"
	IndexFlush  IndexAction = "flush"
)

type IndexCall struct {
	Action IndexAction
	Host   string
	Path   string
	Key    string
}

type CacheIndex struct {
	logger   *slog.Logger
	client   *memcache.Client
	index    map[string]map[string][]string
	lock     sync.RWMutex
	ch       chan *IndexCall
	interval time.Duration
	changed  atomic.Uint64
	wg       sync.WaitGroup
	ctx      context.Context
	cancel   context.CancelFunc
}

func (i *CacheIndex) Start() {
	i.ctx, i.cancel = context.WithCancel(context.Background())
	go i.doAction()
	go i.writeBackTimer()
}

func (i *CacheIndex) Stop() {
	if i.cancel == nil {
		return
	}
	i.logger.Debug("停止缓存索引协程")
	i.cancel()
	close(i.ch)
	i.wg.Wait()
	i.logger.Debug("缓存索引协程停止完成")
}

func (i *CacheIndex) doAppend(action *IndexCall) {
	i.lock.Lock()
	defer i.lock.Unlock()
	hi := i.get(action.Host)
	if ks, ok := hi[action.Path]; ok {
		hi[action.Path] = append(ks, action.Key)
	} else {
		hi[action.Path] = []string{action.Key}
	}
	i.index[action.Host] = hi
	i.changed.Add(1)
}
func (i *CacheIndex) doDelete(action *IndexCall) {
	i.lock.Lock()
	defer i.lock.Unlock()
	hi := i.get(action.Host)
	delete(hi, action.Path)
	i.index[action.Host] = hi
	i.changed.Add(1)
}

func (i *CacheIndex) doAction() {
	i.wg.Add(1)
	defer i.wg.Done()
	defer i.logger.Debug("停止缓存索引动作协程")
	for action := range i.ch {
		switch action.Action {
		case IndexAppend:
			i.doAppend(action)
		case IndexDelete:
			i.doDelete(action)
		case IndexFlush:
			i.doWriteBack()
		}
	}
}

func (i *CacheIndex) doWriteBack() {
	i.lock.RLock()
	defer i.lock.RUnlock()
	i.logger.Debug("开始回写缓存索引", "changed", i.changed.Load())
	for host, idx := range i.index {
		idxKey := fmt.Sprintf("index/%s", host)
		item, err := i.client.Get(idxKey)
		switch err {
		case nil:
		case memcache.ErrCacheMiss:
			item = &memcache.Item{Key: idxKey}
		default:
			i.logger.Error("获取缓存索引失败", "err", err)
			continue
		}
		var buf bytes.Buffer
		writer, err := gzip.NewWriterLevel(&buf, 4)
		if err != nil {
			i.logger.Error("压缩缓存索引失败", "err", err)
			continue
		}
		defer writer.Close()
		if err = gob.NewEncoder(writer).Encode(idx); err != nil {
			i.logger.Error("压缩缓存索引失败", "err", err)
			continue
		}
		writer.Flush()
		item.Value = buf.Bytes()
		err = i.client.CompareAndSwap(item)
		switch err {
		case nil:
			i.logger.Debug("回写缓存索引成功", "host", host, "key", idxKey)
		case memcache.ErrCacheMiss:
			if err := i.client.Set(item); err != nil {
				i.logger.Error("回写缓存索引失败", "err", err, "host", host, "key", idxKey)
			} else {
				i.logger.Debug("回写缓存索引成功", "host", host, "key", idxKey)
			}
		default:
			// TODO:多实例冲突时处理。只影响清理缓存
			i.logger.Error("设置缓存索引失败", "err", err, "host", host, "key", idxKey)
			continue
		}
	}
	i.changed.Store(0)
}

func (i *CacheIndex) writeBackTimer() {
	i.wg.Add(1)
	defer i.wg.Done()
	timer := time.NewTicker(max(i.interval, 5*time.Second))
	defer timer.Stop()
	defer i.logger.Debug("停止缓存索引回写协程")
	for {
		select {
		case <-timer.C:
			if i.changed.Load() > 0 {
				i.doWriteBack()
			}
		case <-i.ctx.Done():
			return
		}
	}
}

func (i *CacheIndex) get(host string) (index map[string][]string) {
	if hi, ok := i.index[host]; ok {
		return hi
	}
	i.logger.Debug("加载缓存索引", "host", host)
	index = make(map[string][]string)
	idxKey := fmt.Sprintf("index/%s", host)
	item, err := i.client.Get(idxKey)
	if err != nil {
		return
	}
	reader, err := gzip.NewReader(bytes.NewBuffer(item.Value))
	if err != nil {
		i.logger.Error("解压缓存索引失败", "err", err)
	} else if err = gob.NewDecoder(reader).Decode(&index); err != nil {
		i.logger.Error("解析缓存索引失败", "err", err)
	}
	return
}

func (i *CacheIndex) Items(host string) iter.Seq2[string, []string] {
	i.lock.RLock()
	defer i.lock.RUnlock()
	hi := i.get(host)
	return func(yield func(string, []string) bool) {
		for key, vs := range hi {
			if !yield(key, vs) {
				return
			}
		}
	}
}

func (i *CacheIndex) Keys(host string) iter.Seq[string] {
	return func(yield func(string) bool) {
		for key := range i.Items(host) {
			if !yield(key) {
				return
			}
		}
	}
}

func (i *CacheIndex) Values(host string) iter.Seq[[]string] {
	return func(yield func([]string) bool) {
		for _, vs := range i.Items(host) {
			if !yield(vs) {
				return
			}
		}
	}
}

func (i *CacheIndex) Get(host, path string) []string {
	i.lock.RLock()
	defer i.lock.RUnlock()
	hi := i.get(host)
	return hi[path]
}

func (i *CacheIndex) DeleteByPaths(host string, paths ...string) {
	for _, p := range paths {
		i.DeleteByPath(host, p)
	}
}

func (i *CacheIndex) DeleteByPath(host, path string) {
	i.ch <- &IndexCall{Action: IndexDelete, Host: host, Path: path}
}

func (i *CacheIndex) Append(host, path, key string) {
	i.ch <- &IndexCall{Action: IndexAppend, Host: host, Path: path, Key: key}
}

func (i *CacheIndex) Flush() {
	i.ch <- &IndexCall{Action: IndexFlush}
}

type Cacher struct {
	client      *memcache.Client
	getCacheKey func(string) string
	logger      *slog.Logger
	index       *CacheIndex
}

func NewCache(mc *Memcached) *Cacher {
	var getCacheKey func(string) string
	if mc.Hashed != nil {
		switch strings.ToLower(*mc.Hashed) {
		case "md5":
			getCacheKey = getHash(md5.New)
		case "sha1":
			getCacheKey = getHash(sha1.New)
		case "sha224":
			getCacheKey = getHash(sha256.New224)
		case "sha256":
			getCacheKey = getHash(sha256.New)
		case "sha384":
			getCacheKey = getHash(sha512.New384)
		case "sha512":
			getCacheKey = getHash(sha512.New)
		}
	}
	client := memcache.New(mc.Address...)
	client.MaxIdleConns = 16
	d := &net.Dialer{
		Timeout:   memcache.DefaultTimeout,
		KeepAlive: 30 * time.Second,
	}
	client.DialContext = d.DialContext
	index := CacheIndex{
		logger:   logger.With("logger", "cache-index"),
		client:   client,
		index:    map[string]map[string][]string{},
		ch:       make(chan *IndexCall, runtime.GOMAXPROCS(0)),
		interval: mc.IndexWriteBack,
	}
	index.Start()
	return &Cacher{
		client:      client,
		getCacheKey: getCacheKey,
		logger:      logger.With("logger", "cache"),
		index:       &index,
	}
}

func (c *Cacher) GetCache(key string) *CachedResp {
	if c.getCacheKey != nil {
		key = c.getCacheKey(key)
	}
	item, err := c.client.Get(key)
	if err != nil {
		c.logger.Info("读取缓存失败", "err", err, "key", key)
		return nil
	}
	var cached CachedResp
	if err = gob.NewDecoder(bytes.NewBuffer(item.Value)).Decode(&cached); err != nil {
		c.logger.Info("解析缓存失败", "err", err, "key", key)
		return nil
	}
	c.logger.Debug("读取缓存成功", "err", err, "key", key)
	return &cached
}

func (c *Cacher) SetCache(host, path, key string, value *CachedResp, ttl time.Duration) {
	if c.getCacheKey != nil {
		key = c.getCacheKey(key)
	}
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(value); err != nil {
		logger.Info("编码缓存数据失败", "err", err)
		return
	}
	var item memcache.Item
	item.Key = key
	item.Value = buf.Bytes()
	item.Expiration = int32(time.Now().Add(ttl).Unix())
	if err := c.client.Set(&item); err != nil {
		c.logger.Info("设置缓存失败", "err", err, "key", key)
	} else {
		c.logger.Debug("设置缓存成功", "ttl", ttl, "expiration", item.Expiration, "key", key)
		c.index.Append(host, path, key)
	}
}

func (c *Cacher) Keys(host string) iter.Seq[string]             { return c.index.Keys(host) }
func (c *Cacher) Values(host string) iter.Seq[[]string]         { return c.index.Values(host) }
func (c *Cacher) Items(host string) iter.Seq2[string, []string] { return c.index.Items(host) }
func (c *Cacher) DeleteByPath(host, path string) {
	for _, k := range c.index.Get(host, path) {
		c.client.Delete(k)
	}
	c.index.DeleteByPath(host, path)
}
func (c *Cacher) DeleteByPaths(host string, paths ...string) {
	for _, path := range paths {
		for _, k := range c.index.Get(host, path) {
			c.client.Delete(k)
		}
	}
	c.index.DeleteByPaths(host, paths...)
}
func (c *Cacher) Flush() { c.index.Flush() }
func (c *Cacher) Close() error {
	c.logger.Debug("停止缓存")
	c.index.Stop()
	c.index.doWriteBack()
	return nil
}

func getHash(new func() hash.Hash) func(string) string {
	return func(s string) string {
		h := new()
		fmt.Fprint(h, s)
		return hex.EncodeToString(h.Sum(nil))
	}
}
