package main

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"strconv"
)

func handlePing(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(200)
	fmt.Fprint(w, os.Getpid())
}

func checkServerExists(filePath string) (ok bool, pid int) {
	if info, err := os.Stat(filePath); os.IsExist(err) || err == nil {
		ok = true
		logger.Debug("现存UnixSocket文件", "mode", info.Mode().Type())
		if info.Mode().Type() == fs.ModeSocket {
			conn, err := net.Dial("unix", filePath)
			if err != nil {
				logger.Info("Unix文件存在但无法连接，将清理文件", "err", err)
				return
			}
			defer conn.Close()
			transport := http.DefaultTransport.(*http.Transport).Clone()
			transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) { return conn, nil }
			client := new(http.Client)
			client.Transport = transport
			resp, err := client.Get("http://localhost/-/ping")
			if err != nil {
				logger.Info("Unix文件存在但无法发送请求，将清理文件", "err", err)
				return
			}
			defer resp.Body.Close()
			if pidData, err := io.ReadAll(resp.Body); err != nil {
				logger.Info("Unix文件存在并且服务存在，但无法解析返回数据", "pid", pidData)
				pid = -1
			} else {
				pid, _ = strconv.Atoi(string(pidData))
				logger.Info("Unix文件存在并且服务存在", "data", pidData, "pid", pid)
				return
			}
		}
	}
	return
}
