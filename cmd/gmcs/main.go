//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gmcs/internal/config"
	"gmcs/internal/server"
)

func main() {
	fmt.Println("Welcome to gmcs 1.21.11!")
	fmt.Println("https://github.com/xhdndmm/gmcs")
	if err := run(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "gmcs.json", "配置文件路径（不存在时生成默认配置）")
	listen := flag.String("listen", "", "TCP 监听地址（覆盖配置文件）")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if *listen != "" {
		cfg.ListenAddress = *listen
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("配置无效（%s）：%w", *configPath, err)
	}

	instance, err := server.New(cfg)
	if err != nil {
		return err
	}

	listener, err := net.Listen("tcp", cfg.ListenAddress)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.ListenAddress, err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGHUP, syscall.SIGTERM)
	defer stop()

	// 可选：pprof 诊断监听（CPU/内存/goroutine 分析，默认关闭）。
	if cfg.PprofAddress != "" {
		stopPprof, err := startPprof(cfg.PprofAddress)
		if err != nil {
			return err
		}
		defer stopPprof()
	}

	slog.Info("gmcs listening",
		"address", listener.Addr().String(), "version", cfg.VersionName, "world", cfg.WorldDir)
	return instance.Serve(ctx, listener)
}

// startPprof 在 addr 上启动 /debug/pprof 监听，返回关闭函数。
func startPprof(addr string) (func(), error) {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("pprof listen on %s: %w", addr, err)
	}
	diagnostic := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := diagnostic.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("pprof listener stopped", "error", err)
		}
	}()
	slog.Info("pprof enabled", "address", listener.Addr().String())
	return func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := diagnostic.Shutdown(shutdownCtx); err != nil {
			slog.Warn("pprof shutdown", "error", err)
		}
		<-done
	}, nil
}
