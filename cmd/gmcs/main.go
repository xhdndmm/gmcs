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
	if err := run(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg := config.Default()
	protocolVersion := flag.Int("protocol", int(cfg.ProtocolVersion), "protocol number shown in server status")
	flag.StringVar(&cfg.ListenAddress, "listen", cfg.ListenAddress, "TCP listen address")
	flag.StringVar(&cfg.MOTD, "motd", cfg.MOTD, "server list message")
	flag.StringVar(&cfg.VersionName, "version", cfg.VersionName, "version name shown in server status")
	flag.IntVar(&cfg.MaxPlayers, "max-players", cfg.MaxPlayers, "maximum players shown in server status")
	flag.IntVar(&cfg.MaxConnections, "max-connections", cfg.MaxConnections, "maximum concurrent client connections")
	flag.Parse()
	if int64(*protocolVersion) > 2147483647 {
		return fmt.Errorf("protocol version must not exceed 2147483647")
	}
	cfg.ProtocolVersion = int32(*protocolVersion)

	if err := cfg.Validate(); err != nil {
		return err
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

	slog.Info("gmcs listening", "address", listener.Addr().String(), "version", cfg.VersionName)
	return instance.Serve(ctx, listener)
}
