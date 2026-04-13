package main

import (
	"flag"
	"log"
	"net"
	"strconv"

	"filegate/config"
	"filegate/internal/server"
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
	log.Printf("FileGate listening on http://%s", addr)

	// 启动
	if err := srv.Run(addr); err != nil {
		log.Fatalf("run server err: %v", err)
	}
}
