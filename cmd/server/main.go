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
	"entity-management/internal/ws"
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

	regionHandler := handler.NewRegionHandler(pool)
	{
		api.GET("/regions", regionHandler.List)
		api.GET("/regions/count", regionHandler.Count)
		api.GET("/regions/:code", regionHandler.Get)
	}

	zoneHandler := handler.NewZoneHandler(pool)
	{
		api.GET("/zones", zoneHandler.List)
		api.POST("/zones", zoneHandler.Create)
		api.POST("/zones/import", zoneHandler.Import)
		api.POST("/zones/from-region", zoneHandler.FromRegion)
		api.GET("/islands", zoneHandler.Islands)
		api.POST("/zones/from-island", zoneHandler.FromIsland)
		api.GET("/zones/:id", zoneHandler.Get)
		api.PUT("/zones/:id", zoneHandler.Update)
		api.DELETE("/zones/:id", zoneHandler.Delete)
	}

	hub := ws.NewHub()

	vehicleHandler := handler.NewVehicleHandler(pool, hub)
	{
		api.GET("/vehicles", vehicleHandler.List)
		api.POST("/vehicles", vehicleHandler.Create)
		api.POST("/vehicles/:id/position", vehicleHandler.UpdatePosition)
		api.GET("/vehicles/stream", vehicleHandler.Stream)
	}

	routeHandler := handler.NewRouteHandler(pool, hub)
	{
		api.GET("/routes", routeHandler.List)
		api.GET("/vehicles/:id/routes", routeHandler.History)
		api.POST("/vehicles/:id/route", routeHandler.Set)
		api.DELETE("/vehicles/:id/route", routeHandler.Clear)
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
