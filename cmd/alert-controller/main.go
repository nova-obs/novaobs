package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"novaobs/internal/alerting"
	"novaobs/internal/config"
	"novaobs/internal/database/mongo"
)

func main() {
	cfg, err := config.Load("configs/config.yaml")
	if err != nil {
		slog.Error("加载配置失败", "error", err)
		os.Exit(1)
	}
	runtimeID := strings.TrimSpace(os.Getenv("NOVAOBS_ALERT_RUNTIME_ID"))
	rulesDirectory := strings.TrimSpace(os.Getenv("NOVAOBS_ALERT_RULES_DIRECTORY"))
	reloadURL := strings.TrimSpace(os.Getenv("NOVAOBS_VMALERT_RELOAD_URL"))
	rulesStatusURL := strings.TrimSpace(os.Getenv("NOVAOBS_VMALERT_RULES_URL"))
	if runtimeID == "" || rulesDirectory == "" || reloadURL == "" || rulesStatusURL == "" {
		slog.Error("告警 Runtime 配置不完整", "required", "NOVAOBS_ALERT_RUNTIME_ID,NOVAOBS_ALERT_RULES_DIRECTORY,NOVAOBS_VMALERT_RELOAD_URL,NOVAOBS_VMALERT_RULES_URL")
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	store, err := mongo.NewStore(ctx, cfg.Database.URI)
	if err != nil {
		slog.Error("连接 MongoDB 失败", "error", err)
		os.Exit(1)
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := store.Close(closeCtx); err != nil {
			slog.Error("关闭 MongoDB 连接失败", "error", err)
		}
	}()
	publisher, err := alerting.NewFileArtifactPublisher(alerting.FilePublisherConfig{RulesDirectory: rulesDirectory, ReloadURL: reloadURL, RulesStatusURL: rulesStatusURL})
	if err != nil {
		slog.Error("初始化 vmalert 发布器失败", "error", err)
		os.Exit(1)
	}
	hostname, _ := os.Hostname()
	workerID := strings.TrimSpace(hostname) + "-" + runtimeID
	reconciler := alerting.NewReconciler(alerting.ReconcilerDependencies{
		Repository: alerting.NewStoreRepository(store.Alerting()),
		Publisher:  publisher,
		WorkerID:   workerID,
		RuntimeID:  runtimeID,
	})
	slog.Info("告警调和器已启动", "runtime_id", runtimeID, "worker_id", workerID)
	run(ctx, reconciler)
}

func run(ctx context.Context, reconciler alerting.Reconciler) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		worked, err := reconciler.ReconcileOnce(ctx)
		if err != nil && ctx.Err() == nil {
			slog.Error("调和告警规则失败", "error", err)
		}
		if worked {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
