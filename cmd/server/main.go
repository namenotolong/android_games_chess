package main

import (
	"context"
	"log"

	"github.com/cloudwego/hertz/pkg/app/server"

	"gomoku-server/internal/config"
	"gomoku-server/internal/game"
	"gomoku-server/internal/httpapi"
	"gomoku-server/internal/storage"
)

func main() {
	cfg := config.Load()
	db, err := storage.OpenSQLite(context.Background(), cfg.DatabasePath)
	if err != nil {
		log.Fatalf("initialize sqlite: %v", err)
	}
	defer db.Close()

	h := server.Default(server.WithHostPorts(cfg.HTTPAddr))
	h.NoHijackConnPool = true
	hub := game.NewHub()
	httpapi.RegisterOnline(h, game.NewService(db), hub)
	h.GET("/healthz", httpapi.HealthHandler(db))

	log.Printf("gomoku service listening on %s (sqlite: %s)", cfg.HTTPAddr, cfg.DatabasePath)
	h.Spin()
}
