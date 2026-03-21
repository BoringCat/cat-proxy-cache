package config

import (
	"os"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/redis/rueidis"
)

type RedisSentinel struct {
	MasterSet string `yaml:"master_set,omitempty"`
	Username  string `yaml:"username,omitempty"`
	Password  string `yaml:"password,omitempty"`
}

type RedisConfig struct {
	Address  []string `yaml:"address"`
	Db       int      `yaml:"db"`
	Username string   `yaml:"username"`
	Password string   `yaml:"password"`

	Sentinel *RedisSentinel `yaml:"sentinel"`

	DialTimeout     time.Duration `yaml:"dial_timeout,omitempty"`
	KeepAlive       time.Duration `yaml:"keep_alive,omitempty"`
	WriteTimeout    time.Duration `yaml:"write_timeout,omitempty"`
	ConnMaxLifetime time.Duration `yaml:"conn_max_lifetime,omitempty"`
	MaxFlushDelay   time.Duration `yaml:"max_flush_delay,omitempty"`

	DisableTCPNoDelay bool `yaml:"disable_tcp_no_delay,omitempty"`
	ClientNoTouch     bool `yaml:"client_no_touch,omitempty"`
	DisableRetry      bool `yaml:"disable_retry,omitempty"`
	DisableCache      bool `yaml:"disable_cache,omitempty"`

	ShardsRefreshInterval time.Duration `yaml:"shards_refresh_interval,omitempty"`
	MaxMovedRedirections  int           `yaml:"max_moved_redirections,omitempty"`

	ItemsPerScan int64 `yaml:"items_per_scan,omitempty"`

	client rueidis.Client
}

func (c *RedisConfig) GetClient(shared bool) (client rueidis.Client, err error) {
	if c.client != nil && shared {
		return c.client, nil
	}
	opt := rueidis.ClientOption{
		Username:          c.Username,
		Password:          c.Password,
		InitAddress:       c.Address,
		SelectDB:          c.Db,
		ConnWriteTimeout:  c.WriteTimeout,
		ConnLifetime:      c.ConnMaxLifetime,
		MaxFlushDelay:     c.MaxFlushDelay,
		DisableTCPNoDelay: c.DisableTCPNoDelay,
		ClientNoTouch:     c.ClientNoTouch,
		DisableRetry:      c.DisableRetry,
		DisableCache:      c.DisableCache,
	}
	opt.Dialer.Timeout = c.DialTimeout
	opt.Dialer.KeepAlive = c.KeepAlive
	opt.ClusterOption.ShardsRefreshInterval = c.ShardsRefreshInterval
	opt.ClusterOption.MaxMovedRedirections = c.MaxMovedRedirections
	if c.Sentinel != nil {
		opt.Sentinel.Dialer.Timeout = c.DialTimeout
		opt.Sentinel.Dialer.KeepAlive = c.KeepAlive
		opt.Sentinel.MasterSet = c.Sentinel.MasterSet
		opt.Sentinel.Username = c.Sentinel.Username
		opt.Sentinel.Password = c.Sentinel.Password
	}
	if client, err = rueidis.NewClient(opt); err != nil {
		return
	} else if c.client == nil {
		c.client = client
	}
	return
}

type MemcachedConfig struct {
	Address []string `yaml:"address"`
	Hashed  *string  `yaml:"hashed"`
}

type Model struct {
	Upstream    *string                `yaml:"upstream"`
	TTL         *map[int]time.Duration `yaml:"ttl,omitempty"`
	MaxRedirect *int                   `yaml:"max_redirect"`
	Redis       *RedisConfig           `yaml:"redis"`
}

type Path struct {
	Model  `yaml:",inline"`
	Prefix string `yaml:"prefix"`
	Expr   string `yaml:"expr,omitempty"`
	Repl   string `yaml:"repl,omitempty"`
}

type VServer struct {
	Model `yaml:",inline"`
	Host  string  `yaml:"host,omitempty"`
	Paths []*Path `yaml:"paths"`
}

type IndexConfig struct {
	Backend string `yaml:"backend"`
	Redis   *RedisConfig
}

type BlobOption struct {
	CacheKey string `yaml:"cache_key,omitempty"`
}

type BlobConfig struct {
	Backend    string           `yaml:"backend"`
	Redis      *RedisConfig     `yaml:"redis"`
	Memcached  *MemcachedConfig `yaml:"memcached"`
	BlobOption `yaml:"-,inline"`
}

type CacheConfig struct {
	Index *IndexConfig
	Blob  *BlobConfig
}

type Config struct {
	Cache   *CacheConfig `yaml:"cache"`
	Servers []*VServer   `yaml:"servers"`
}

func LoadConfig(fpath string) *Config {
	fd, err := os.Open(fpath)
	if err != nil {
		panic(err)
	}
	defer fd.Close()
	var conf Config
	if err := yaml.NewDecoder(fd).Decode(&conf); err != nil {
		panic(err)
	}
	return &conf
}
