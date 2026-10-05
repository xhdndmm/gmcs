//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	slog.Info("gmcs listening",
		"address", listener.Addr().String(), "version", cfg.VersionName, "world", cfg.WorldDir)
	return instance.Serve(ctx, listener)
}
