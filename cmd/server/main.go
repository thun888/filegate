package main

import (
	"flag"
	"log"
	"net"
	"os"
	"strconv"

	"github.com/thun888/filegate/config"
	"github.com/thun888/filegate/internal/server"
)

func main() {
	configPath := flag.String("config", "config.yaml", "Path to config")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	// 初始化
	srv, err := server.New(cfg)
	if err != nil {
		log.Fatalf("initialize server err: %v", err)
	}

	addr := net.JoinHostPort(cfg.System.Server.Host, strconv.Itoa(cfg.System.Server.Port))
	log.Printf("FileGate %s listening on http://%s (FILEGATE_DEBUG=%q)", server.Version, addr, os.Getenv("FILEGATE_DEBUG"))

	// 启动
	if err := srv.Run(addr); err != nil {
		log.Fatalf("run server err: %v", err)
	}
}
