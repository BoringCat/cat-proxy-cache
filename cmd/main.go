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
	"sync"
	"syscall"

	"github.com/alecthomas/kingpin/v2"
	"github.com/boringcat/cat-proxy-cache/cache/cacher"
	"github.com/boringcat/cat-proxy-cache/config"
	"github.com/boringcat/cat-proxy-cache/server"
	"github.com/boringcat/cat-proxy-cache/utils"
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
	wg        *sync.WaitGroup

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
	slog.SetDefault(logger)
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
		if fileok, pid := checkServerExists(listens[1]); fileok && pid == 0 {
			os.Remove(listens[1])
		}
		if listen, err = net.Listen("unix", listens[1]); err != nil {
			logger.Error("创建监听失败", "listen", listens[1], "err", err)
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
			logger.Error("创建监听失败", "listen", listens[1], "err", err)
			return
		}
	default:
		if listen, err = net.Listen("tcp", listenAddr); err != nil {
			logger.Error("创建监听失败", "listen", listenAddr, "err", err)
			return
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
	prometheus.MustRegister(request_time, cached_total, response_data)
	sch := make(chan os.Signal, runtime.NumCPU())
	signal.Notify(sch, syscall.SIGINT, syscall.SIGTERM)
	go handleSignal(sch)
	conf := config.LoadConfig(configFile)
	r := mux.NewRouter().StrictSlash(true)
	r.HandleFunc("/-/ping", handlePing)
	r.Use(prometheusMiddleware)
	if len(metricAddr) > 0 {
		go startMetricServer()
	} else {
		r.Handle("/metrics", promhttp.Handler())
	}
	cache, err := cacher.NewCacher(conf.Cache)
	if err != nil {
		panic(err)
	}
	for _, vs := range conf.Servers {
		for _, p := range vs.Paths {
			var svc *server.Server
			redirect := utils.OrderValue(p.MaxRedirect, vs.MaxRedirect)
			if svc, err = server.NewServer(server.ServerOpt{
				Vserver:     vs,
				Path:        p,
				Cache:       cache,
				MaxRedirect: redirect,
			}); err != nil {
				logger.Error("创建VServer失败", "err", err)
				continue
			}
			prefix, _ := strings.CutSuffix(p.Prefix, "/")
			prefix = fmt.Sprint(prefix, "/")
			if err != nil {
				logger.Error("创建路径监听失败", "err", err, "host", vs.Host, "prefix", prefix)
				continue
			}
			perr, cerr := svc.HandleRoute(r.PathPrefix(prefix), r.PathPrefix(prefix), vs.Host)
			logger.Debug("创建路径监听", "prefix", prefix, "upstream", *utils.OrderValue(p.Upstream, vs.Upstream), "host", vs.Host)
			if perr != nil {
				logger.Error("创建数据路由失败", "err", perr, "host", vs.Host, "prefix", prefix)
			}
			if cerr != nil {
				logger.Error("创建清理路由失败", "err", cerr, "host", vs.Host, "prefix", prefix)
			}
		}
	}
	wg = new(sync.WaitGroup)
	for _, addr := range listenAddrs {
		wg.Go(func() { startListenServer(addr, r) })
	}
	wg.Wait()
}
