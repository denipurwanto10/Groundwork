package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"entity-management/internal/config"
	"entity-management/internal/db"
	"entity-management/internal/handler"
	"github.com/gin-gonic/gin"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	pool, err := db.NewPool(&cfg.Database)
	if err != nil {
		log.Fatalf("connect database: %v", err)
	}
	defer pool.Close()

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(gin.Logger())

	healthHandler := handler.NewHealthHandler(pool)
	r.GET("/health", healthHandler.Health)

	entityHandler := handler.NewEntityHandler(pool)
	api := r.Group("/api")
	{
		api.GET("/entities", entityHandler.List)
		api.GET("/entities/:id", entityHandler.Get)
		api.POST("/entities", entityHandler.Create)
		api.PUT("/entities/:id", entityHandler.Update)
		api.DELETE("/entities/:id", entityHandler.Delete)
	}

	srv := &http.Server{
		Addr:         ":" + cfg.Server.Port,
		Handler:      r,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
		IdleTimeout:  cfg.Server.IdleTimeout,
	}

	go func() {
		log.Printf("server starting on port %s", cfg.Server.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("server forced to shutdown: %v", err)
	}

	log.Println("server exited")
}
