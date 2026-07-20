package main

import (
	"fmt"
	"log/slog"
	"os"

	"novaapm/internal/app"
	"novaapm/internal/config"
)

func main() {
	cfg, err := config.Load("configs/config.yaml")
	if err != nil {
		slog.Error("加载配置失败", "error", err)
		os.Exit(1)
	}

	server, err := app.New(cfg)
	if err != nil {
		slog.Error("初始化服务失败", "error", err)
		os.Exit(1)
	}

	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	if err := server.Run(addr); err != nil {
		slog.Error("服务退出", "error", err)
		os.Exit(1)
	}
}
