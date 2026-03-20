package main

import (
	"os"
	"time"

	"github.com/goccy/go-yaml"
)

type Rediscached struct {
	IsCluster bool     `yaml:"cluster"`
	Db        int      `yaml:"db"`
	Address   []string `yaml:"address"`
	Username  string   `yaml:"username"`
	Password  string   `yaml:"password"`

	DialTimeout     time.Duration `yaml:"dial_timeout,omitempty"`
	ReadTimeout     time.Duration `yaml:"read_timeout,omitempty"`
	WriteTimeout    time.Duration `yaml:"write_timeout,omitempty"`
	MaxRedirects    int           `yaml:"max_redirects,omitempty"`
	PoolSize        int           `yaml:"pool_size,omitempty"`
	PoolTimeout     time.Duration `yaml:"pool_timeout,omitempty"`
	MinIdleConns    int           `yaml:"min_idle_conns,omitempty"`
	MaxIdleConns    int           `yaml:"max_idle_conns,omitempty"`
	MaxActiveConns  int           `yaml:"max_active_conns,omitempty"`
	ConnMaxIdleTime time.Duration `yaml:"conn_max_idle_time,omitempty"`
	ConnMaxLifetime time.Duration `yaml:"conn_max_lifetime,omitempty"`

	ItemsPerScan int64 `yaml:"items_per_scan,omitempty"`

	ReadBufferSize  int `yaml:"read_buffer_size,omitempty"`
	WriteBufferSize int `yaml:"write_buffer_size,omitempty"`

	client *Cacher
}

func (m *Rediscached) New() *Cacher {
	if m.client != nil {
		return m.client
	}
	m.client = NewCache(m)
	return m.client
}

type Model struct {
	Upstream    *string                `yaml:"upstream"`
	TTL         *map[int]time.Duration `yaml:"ttl,omitempty"`
	MaxRedirect *int                   `yaml:"max_redirect"`
	CacheKey    *string                `yaml:"cache_key,omitempty"`
	Redis       *Rediscached           `yaml:"redis"`
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

type Config struct {
	Redis   *Rediscached `yaml:"redis"`
	Servers []*VServer   `yaml:"servers"`
}

func loadConfig(fpath string) *Config {
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
