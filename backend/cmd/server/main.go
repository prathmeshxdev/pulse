package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/prathmeshxdev/pulse/internal/api"
	"github.com/prathmeshxdev/pulse/internal/chclient"
	"github.com/prathmeshxdev/pulse/internal/config"
)

func main() {
	cfg := config.LoadServerConfig()
	ctx := context.Background()

	conn, err := chclient.Connect(ctx, cfg.ClickHouseDSN)
	if err != nil {
		log.Fatalf("clickhouse: %v", err)
	}
	defer conn.Close()

	var rdb *redis.Client
	if (cfg.PreflightEnabled || cfg.LiveEnabled) && cfg.RedisAddr != "" {
		rdb = redis.NewClient(&redis.Options{Addr: cfg.RedisAddr, Password: cfg.RedisPassword})
		if err := rdb.Ping(ctx).Err(); err != nil {
			log.Printf("redis unavailable (%v); preflight cache and live-concurrency disabled", err)
			rdb = nil
		}
	}

	srv := api.New(cfg, conn, rdb)
	httpSrv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		log.Printf("sony-liv concurrency API listening on %s", cfg.Addr)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
	_ = srv.Shutdown(shutdownCtx) // flush OTel exporter

}
