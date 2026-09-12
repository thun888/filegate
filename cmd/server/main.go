package main

import (
	"flag"
	"log/slog"
	"net"
	"os"
	"strconv"

	"github.com/thun888/filegate/config"
	"github.com/thun888/filegate/internal/logging"
	"github.com/thun888/filegate/internal/server"
)

func main() {
	configPath := flag.String("config", "config.yaml", "Path to config")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}

	level, err := logging.ParseLevel(cfg.System.Logging.Level)
	if err != nil {
		slog.Error("parse system.logging.level", "error", err)
		os.Exit(1)
	}
	if os.Getenv("FILEGATE_DEBUG") != "" {
		level = slog.LevelDebug
	}
	logger := logging.New(level, os.Stderr)
	slog.SetDefault(logger)

	// 初始化
	srv, err := server.New(cfg)
	if err != nil {
		slog.Error("initialize server", "error", err)
		os.Exit(1)
	}

	addr := net.JoinHostPort(cfg.System.Server.Host, strconv.Itoa(cfg.System.Server.Port))
	logger.Info("FileGate listening",
		"version", server.Version,
		"addr", "http://"+addr,
		"filegate_debug", os.Getenv("FILEGATE_DEBUG"))

	// 启动
	if err := srv.Run(addr); err != nil {
		slog.Error("run server", "error", err)
		os.Exit(1)
	}
}
