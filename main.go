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
	configFile  string
	listenAddrs []string
	metricAddr  string
	logLevel    string

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
	app.Flag("listen", "监听地址").Short('l').Required().StringsVar(&listenAddrs)
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

func startListenServer(listenAddr string, h http.Handler) (err error) {
	var listen net.Listener
	listens := strings.SplitN(listenAddr, ":", 2)
	switch listens[0] {
	case "unix":
		if listen, err = net.Listen("unix", listens[1]); err != nil {
			return
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
	logger.Info("服务启动", "listenAddr", listenAddr, "version", version, "gitBranch", gitBranch, "commit", commit)
	server := &http.Server{Handler: handlers.RecoveryHandler()(h)}
	callbacks = append(callbacks, server.Close)
	err = server.Serve(listen)
	if err != nil && err == http.ErrServerClosed {
		err = nil
	}
	return
}

func main() {
	parseArgs()
	prometheus.MustRegister(
		request_time, cached_total, response_data, cache_key_length,
		prune_scan_histogram,
	)
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
	for _, vs := range conf.Servers {
		for _, p := range vs.Paths {
			var server *Server
			var err error
			cache := orderValue(p.Redis, vs.Redis, conf.Redis).New()
			redirect := orderValue(p.FollowRedirect, vs.FollowRedirect)
			server, err = NewServer(ServerOpt{vs, p, cache, redirect != nil && !*redirect})
			prefix, _ := strings.CutSuffix(p.Prefix, "/")
			prefix = fmt.Sprint(prefix, "/")
			if err != nil {
				logger.Error("创建路径监听失败", "err", err, "host", vs.Host, "prefix", prefix)
				return
			}
			perr, cerr := server.HandleRoute(r.PathPrefix(prefix), r.PathPrefix(prefix), vs.Host)
			logger.Debug("创建路径监听", "prefix", prefix, "upstream", *orderValue(p.Upstream, vs.Upstream), "host", vs.Host)
			if perr != nil {
				logger.Error("创建数据路由失败", "err", perr, "host", vs.Host, "prefix", prefix)
			}
			if cerr != nil {
				logger.Error("创建清理路由失败", "err", cerr, "host", vs.Host, "prefix", prefix)
			}
		}
	}
	var ctx context.Context
	ctx, waitStop = context.WithCancel(context.TODO())
	for _, addr := range listenAddrs {
		go startListenServer(addr, r)
	}
	<-ctx.Done()
}
