package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"entity-management/internal/auth"
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
	if err := cfg.Validate(); err != nil {
		log.Fatalf("invalid config: %v", err)
	}

	pool, err := db.NewPool(&cfg.Database)
	if err != nil {
		log.Fatalf("connect database: %v", err)
	}
	defer pool.Close()

	authService := auth.NewService(pool, cfg.Auth.Secret, cfg.Auth.TTL)
	authMiddleware := auth.NewMiddleware(authService)

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(gin.Logger())

	healthHandler := handler.NewHealthHandler(pool)
	authHandler := handler.NewAuthHandler(authService)
	r.GET("/health", healthHandler.Health)

	// Public auth endpoint. Accounts are created by an admin from the Users tab,
	// never by the visitor.
	api := r.Group("/api")
	{
		api.POST("/auth/login", authHandler.Login)

		// Protected routes (require valid JWT)
		protected := api.Group("", authMiddleware.Require())
		{
			// Reads are open to every signed-in role; writes are admin-only.
			adminOnly := authMiddleware.RequireRole(auth.RoleAdmin)

			entityHandler := handler.NewEntityHandler(pool)
			protected.GET("/entities", entityHandler.List)
			protected.GET("/entities/:id", entityHandler.Get)
			protected.POST("/entities", adminOnly, entityHandler.Create)
			protected.PUT("/entities/:id", adminOnly, entityHandler.Update)
			protected.DELETE("/entities/:id", adminOnly, entityHandler.Delete)

			regionHandler := handler.NewRegionHandler(pool)
			protected.GET("/regions", regionHandler.List)
			protected.GET("/regions/count", regionHandler.Count)
			protected.GET("/regions/:code", regionHandler.Get)

			// Accounts: the list feeds the driver picker, the CRUD behind it
			// is the admin "Kelola User" screen.
			userHandler := handler.NewUserHandler(authService)
			protected.GET("/users", adminOnly, userHandler.List)
			protected.POST("/users", adminOnly, userHandler.Create)
			protected.PUT("/users/:id", adminOnly, userHandler.Update)
			protected.DELETE("/users/:id", adminOnly, userHandler.Delete)

			zoneHandler := handler.NewZoneHandler(pool)
			protected.GET("/zones", zoneHandler.List)
			protected.POST("/zones", adminOnly, zoneHandler.Create)
			protected.POST("/zones/import", adminOnly, zoneHandler.Import)
			protected.POST("/zones/from-region", adminOnly, zoneHandler.FromRegion)
			protected.GET("/islands", zoneHandler.Islands)
			protected.POST("/zones/from-island", adminOnly, zoneHandler.FromIsland)
			protected.GET("/zones/:id", zoneHandler.Get)
			protected.PUT("/zones/:id", adminOnly, zoneHandler.Update)
			protected.DELETE("/zones/:id", adminOnly, zoneHandler.Delete)

			hub := ws.NewHub()

			vehicleHandler := handler.NewVehicleHandler(pool, hub)
			protected.GET("/vehicles", vehicleHandler.List)
			protected.POST("/vehicles", adminOnly, vehicleHandler.Create)
			protected.PUT("/vehicles/:id", adminOnly, vehicleHandler.Update)
			protected.DELETE("/vehicles/:id", adminOnly, vehicleHandler.Delete)
			protected.POST("/vehicles/:id/position", vehicleHandler.UpdatePosition)
			protected.GET("/vehicles/stream", vehicleHandler.Stream)

			// Crossing history of the zone boundaries. Every signed-in role may
			// read it; a driver only sees the crossings of their own unit.
			geofenceHandler := handler.NewGeofenceHandler(pool)
			protected.GET("/geofence-events", geofenceHandler.List)

			routeHandler := handler.NewRouteHandler(pool, hub)
			protected.GET("/routes", routeHandler.List)
			protected.GET("/vehicles/:id/routes", routeHandler.History)
			protected.POST("/vehicles/:id/route", routeHandler.Set)
			protected.DELETE("/vehicles/:id/route", routeHandler.Clear)
		}
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
