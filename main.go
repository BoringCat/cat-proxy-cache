package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"slices"
	"strings"
	"syscall"

	"github.com/alecthomas/kingpin/v2"
	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	configFile string
	listenAddr string
	metricAddr string
	logLevel   string

	logger    *slog.Logger
	callbacks []func() error
	waitStop  context.CancelFunc

	version, buildDate, commit, goVersion, gitBranch string
)

func getVersionStr() string {
	return fmt.Sprintf(
		"%s, version %s (branch: %s, revision: %s)\n  build date:\t%s\n  go version:\t%s\n  platform:\t%s/%s",
		"prom-tsdb-copyer", version, gitBranch, commit, buildDate, goVersion, runtime.GOOS, runtime.GOARCH,
	)
}

func parseArgs() {
	app := kingpin.New("代理缓存", "")
	app.HelpFlag.Short('h')
	app.Version(getVersionStr()).VersionFlag.Short('v')
	app.Flag("config", "配置文件").Short('c').Required().ExistingFileVar(&configFile)
	app.Flag("listen", "监听地址").Short('l').Required().StringVar(&listenAddr)
	app.Flag("metrics-listen", "监听地址").StringVar(&metricAddr)
	app.Flag("log-level", "日志等级").Default("WARN").EnumVar(&logLevel, "DEBUG", "INFO", "WARN", "ERROR")

	kingpin.MustParse(app.Parse(os.Args[1:]))
	var opts slog.HandlerOptions
	switch logLevel {
	case "DEBUG":
		opts.Level = slog.LevelDebug
	case "INFO":
		opts.Level = slog.LevelInfo
	case "WARN":
		opts.Level = slog.LevelWarn
	case "ERROR":
		opts.Level = slog.LevelError
	}
	logger = slog.New(slog.NewTextHandler(os.Stderr, &opts))
}

func stop() {
	slices.Reverse(callbacks)
	for _, fn := range callbacks {
		fn()
	}
	callbacks = nil
	defer waitStop()
}

func handleSignal(ch <-chan os.Signal) {
	<-ch
	stop()
}

func newHTTPClient() (*http.Client, *http.Client) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = 10
	originDialContext := transport.DialContext
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := originDialContext(ctx, network, addr)
		logger.Info("发起上游连接", "network", network, "addr", addr, "RemoteAddr", conn.RemoteAddr(), "LocalAddr", conn.LocalAddr(), "err", err)
		return conn, err
	}
	client := new(http.Client)
	client.Transport = transport
	noRedriectClient := new(http.Client)
	noRedriectClient.Transport = transport
	return client, noRedriectClient
}

func main() {
	parseArgs()
	prometheus.MustRegister(request_time, cached_total, response_data, cache_key_length)
	sch := make(chan os.Signal, runtime.NumCPU())
	signal.Notify(sch, syscall.SIGINT, syscall.SIGTERM)
	go handleSignal(sch)
	conf := loadConfig(configFile)
	r := mux.NewRouter().StrictSlash(true)
	r.Use(prometheusMiddleware)
	if len(metricAddr) > 0 {
		go startMetricServer()
	} else {
		r.Handle("/metrics", promhttp.Handler())
	}
	client, noRedriectClient := newHTTPClient()
	for _, vs := range conf.Servers {
		for _, p := range vs.Paths {
			var server, prune http.HandlerFunc
			var err error
			mc, isNew := orderValue(p.Memcached, vs.Memcached, conf.Memcached).New()
			if isNew {
				callbacks = append(callbacks, mc.Close)
			}
			redirect := orderValue(p.FollowRedirect, vs.FollowRedirect)
			if redirect != nil && !*redirect {
				server, prune, err = NewServer(vs, p, noRedriectClient, mc)
			} else {
				server, prune, err = NewServer(vs, p, client, mc)
			}
			prefix, _ := strings.CutSuffix(p.Prefix, "/")
			prefix = fmt.Sprint(prefix, "/")
			if err != nil {
				logger.Error("创建路径监听失败", "err", err, "host", vs.Host, "prefix", prefix)
				return
			}
			sr := r.PathPrefix(prefix).Methods(http.MethodGet, http.MethodHead).Handler(server)
			pr := r.PathPrefix(prefix).Methods("PRUNE").Handler(prune)
			if len(vs.Host) > 0 {
				sr.Host(vs.Host)
				pr.Host(vs.Host)
			}
			logger.Debug("创建路径监听", "prefix", prefix, "upstream", *orderValue(p.Upstream, vs.Upstream), "host", vs.Host)
			if err := sr.GetError(); err != nil {
				logger.Error("创建数据路由失败", "err", err, "host", vs.Host, "prefix", prefix)
			}
			if err := pr.GetError(); err != nil {
				logger.Error("创建清理路由失败", "err", err, "host", vs.Host, "prefix", prefix)
			}
		}
	}
	var err error
	var listen net.Listener
	var ctx context.Context
	listens := strings.SplitN(listenAddr, ":", 2)
	ctx, waitStop = context.WithCancel(context.TODO())
	switch listens[0] {
	case "unix":
		if listen, err = net.Listen("unix", listens[1]); err != nil {
			panic(err)
		}
		if !strings.HasPrefix(listens[1], "@") {
			os.Chmod(listens[1], 0o666)
		}
		callbacks = append(callbacks, func() error {
			return os.Remove(listens[1])
		})
	case "tcp":
		if listen, err = net.Listen("tcp", listens[1]); err != nil {
			panic(err)
		}
	default:
		if listen, err = net.Listen("tcp", listenAddr); err != nil {
			panic(err)
		}
	}
	callbacks = append(callbacks, listen.Close)
	logger.Info("服务启动", "listenAddr", listenAddr)
	server := &http.Server{Handler: handlers.RecoveryHandler()(r)}
	callbacks = append(callbacks, server.Close)
	if err = server.Serve(listen); err != nil && err != http.ErrServerClosed {
		panic(err)
	}
	<-ctx.Done()
}
