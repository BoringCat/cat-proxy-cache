package main

import (
	"os"
	"time"

	"github.com/goccy/go-yaml"
)

type Memcached struct {
	Hashed         *string       `yaml:"hashed"`
	Address        []string      `yaml:"address"`
	IndexWriteBack time.Duration `yaml:"index_write_back"`

	client *Cacher
}

func (m *Memcached) New() (*Cacher, bool) {
	if m.client != nil {
		return m.client, false
	}
	m.client = NewCache(m)
	return m.client, true
}

type Model struct {
	Upstream       *string                `yaml:"upstream"`
	TTL            *map[int]time.Duration `yaml:"ttl,omitempty"`
	FollowRedirect *bool                  `yaml:"follow_redirect"`
	CacheKey       *string                `yaml:"cache_key,omitempty"`
	Memcached      *Memcached             `yaml:"memcached"`
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
	Memcached *Memcached `yaml:"memcached"`
	Servers   []*VServer `yaml:"servers"`
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
