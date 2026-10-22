package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"entity-management/internal/audit"
	"entity-management/internal/auth"
	"entity-management/internal/config"
	"entity-management/internal/db"
	"entity-management/internal/handler"
	"entity-management/internal/middleware"
	"entity-management/internal/push"
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

	authService := auth.NewServiceWithRefresh(pool, cfg.Auth.Secret, cfg.Auth.TTL, cfg.Auth.RefreshTTL)
	authMiddleware := auth.NewMiddleware(authService)

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(gin.Logger())
	if cfg.Server.Compression {
		// The zone and region answers are large and compress about ten to
		// one; a small answer is left as it is.
		r.Use(middleware.Gzip(cfg.Server.CompressThreshold))
	}

	healthHandler := handler.NewHealthHandler(pool)
	authHandler := handler.NewAuthHandler(authService)
	r.GET("/health", healthHandler.Health)

	// Public auth endpoints. Accounts are created by an admin from the Users
	// tab, never by the visitor. The only exception is the very first one:
	// a fresh workspace needs an admin before anybody can sign in.
	api := r.Group("/api")
	{
		api.POST("/auth/login", authHandler.Login)
		api.POST("/auth/refresh", authHandler.Refresh)
		api.GET("/auth/status", authHandler.Status)
		api.POST("/auth/setup", authHandler.Setup)

		// Protected routes (require valid JWT)
		protected := api.Group("", authMiddleware.Require())
		{
			// The trail of who changed what. It runs after the auth
			// middleware, so it knows which account made the request, and
			// it records every successful write on its own: no handler has
			// to remember to log.
			auditRecorder := audit.NewRecorder(pool)
			protected.Use(auditRecorder.Middleware())

			// Reads are open to every signed-in role; writes are admin-only.
			adminOnly := authMiddleware.RequireRole(auth.RoleAdmin)

			// One hub for every live frame: positions, routes and the zone
			// counters that change while the operator works.
			hub := ws.NewHub()

			// Web Push: the notifications that reach the operator even
			// when the tab of the app is closed. Without the VAPID keys the
			// feature is simply off — the server runs as before and sends
			// nothing.
			pushConfig := push.Config{
				PublicKey:  cfg.Push.PublicKey,
				PrivateKey: cfg.Push.PrivateKey,
				Subject:    cfg.Push.Subject,
			}
			var pushSender push.Sender
			if pushConfig.Enabled() {
				pushSender = push.NewSender(pushConfig)
				log.Printf("web push is on (subject %s)", pushConfig.Subject)
			} else {
				log.Print("web push is off: VAPID_PUBLIC_KEY and VAPID_PRIVATE_KEY are not set")
			}
			pushService := push.NewService(pool, pushConfig, pushSender)

			entityHandler := handler.NewEntityHandler(pool, hub)
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
			protected.GET("/zones/counts", zoneHandler.Counts)
			protected.POST("/zones", adminOnly, zoneHandler.Create)
			protected.POST("/zones/import", adminOnly, zoneHandler.Import)
			protected.POST("/zones/from-region", adminOnly, zoneHandler.FromRegion)
			protected.GET("/islands", zoneHandler.Islands)
			protected.POST("/zones/from-island", adminOnly, zoneHandler.FromIsland)
			protected.GET("/zones/:id", zoneHandler.Get)
			protected.PUT("/zones/:id", adminOnly, zoneHandler.Update)
			protected.DELETE("/zones/:id", adminOnly, zoneHandler.Delete)

			vehicleHandler := handler.NewVehicleHandler(pool, hub, pushService)
			protected.GET("/vehicles", vehicleHandler.List)
			protected.POST("/vehicles", adminOnly, vehicleHandler.Create)
			protected.PUT("/vehicles/:id", adminOnly, vehicleHandler.Update)
			protected.DELETE("/vehicles/:id", adminOnly, vehicleHandler.Delete)
			protected.POST("/vehicles/:id/position", vehicleHandler.UpdatePosition)
			protected.GET("/vehicles/:id/history", vehicleHandler.History)
			protected.GET("/vehicles/stream", vehicleHandler.Stream)

			// A unit that stops reporting must not look alive forever. The
			// sweep puts the silent ones into the offline state; a unit that
			// reports again lifts itself back up, because every position
			// report writes a fresh status.
			if cfg.Fleet.OfflineAfter > 0 {
				vehicleHandler.SetOfflineAfter(cfg.Fleet.OfflineAfter)
				go sweepOfflineVehicles(vehicleHandler)
				log.Printf("units that stay silent for %s count as offline", cfg.Fleet.OfflineAfter)
			}

			// A unit that keeps reporting the same spot is standing still.
			// The server decides that on its own: it keeps the anchor of
			// every unit up to date with every report, and the sweep turns a
			// unit that stood still long enough into an idle one.
			if cfg.Fleet.IdleAfter > 0 {
				vehicleHandler.SetIdleDetection(cfg.Fleet.IdleAfter, cfg.Fleet.IdleRadiusM, cfg.Fleet.IdleMaxSpeedKmh)
				go sweepIdleVehicles(vehicleHandler)
				log.Printf("units that stand still for %s count as idle (radius %gm, speed %gkm/h)",
					cfg.Fleet.IdleAfter, cfg.Fleet.IdleRadiusM, cfg.Fleet.IdleMaxSpeedKmh)
			}

			// Crossing history of the zone boundaries. Every signed-in role may
			// read it; a driver only sees the crossings of their own unit.
			geofenceHandler := handler.NewGeofenceHandler(pool)
			protected.GET("/geofence-events", geofenceHandler.List)

			// The idle sessions of the fleet: how long a unit stood still, and
			// where it stood. A driver only sees the sessions of their own
			// unit.
			idleHandler := handler.NewIdleHandler(pool)
			protected.GET("/idle-events", idleHandler.List)

			routeHandler := handler.NewRouteHandler(pool, hub, pushService)
			protected.GET("/routes", routeHandler.List)
			protected.GET("/vehicles/:id/routes", routeHandler.History)
			protected.GET("/vehicles/:id/route", routeHandler.Active)
			protected.POST("/vehicles/:id/route", routeHandler.Set)
			protected.POST("/vehicles/:id/route/start", routeHandler.Start)
			protected.DELETE("/vehicles/:id/route", routeHandler.Clear)

			// The stops of a route are steps of the trip: the driver
			// confirms the one the unit is serving, or passes it by.
			protected.POST("/vehicles/:id/route/stops/:stop_id/complete", routeHandler.CompleteStop)
			protected.POST("/vehicles/:id/route/stops/:stop_id/skip", routeHandler.SkipStop)

			// The aggregate of the day, for the Overview tab. A driver only
			// sees their own unit, the same rule the fleet list follows.
			overviewHandler := handler.NewOverviewHandler(pool)
			protected.GET("/overview", overviewHandler.Get)

			// Work orders: the jobs the fleet is sent out for. Everybody may
			// read the board (a driver only sees their own tasks); writing a
			// task is admin-only, while moving one along is the work of the
			// field: a driver or a staff member does that.
			taskHandler := handler.NewTaskHandler(pool, pushService)
			protected.GET("/tasks", taskHandler.List)
			protected.GET("/tasks/:id", taskHandler.Get)
			protected.POST("/tasks", adminOnly, taskHandler.Create)
			protected.PUT("/tasks/:id", adminOnly, taskHandler.Update)
			protected.DELETE("/tasks/:id", adminOnly, taskHandler.Delete)
			protected.PUT("/tasks/:id/status",
				authMiddleware.RequireRole(auth.RoleAdmin, auth.RoleStaff, auth.RoleDriver),
				taskHandler.UpdateStatus)

			// The trail of successful writes. Admin-only: it names the
			// accounts of the workspace.
			auditHandler := handler.NewAuditHandler(auditRecorder)
			protected.GET("/audit-logs", adminOnly, auditHandler.List)

			// Web Push: the subscriptions of the browsers of the
			// operator, the public key of the server and the test message.
			// Every signed-in role may use them: the one who asked for the
			// notifications is the one who receives them.
			pushHandler := handler.NewPushHandler(pushService)
			protected.GET("/push/vapid-public-key", pushHandler.PublicKey)
			protected.POST("/push/subscribe", pushHandler.Subscribe)
			protected.DELETE("/push/subscribe", pushHandler.Unsubscribe)
			protected.POST("/push/test", pushHandler.Test)

			// The operational report: what the fleet did in a period, as
			// numbers and as a CSV file. Every role may read it, but a
			// driver only reads the report of their own unit.
			reportHandler := handler.NewReportHandler(pool)
			protected.GET("/reports", reportHandler.Get)
			protected.GET("/reports/export.csv", reportHandler.ExportCSV)
		}
	}

	srv := &http.Server{
		Addr:         cfg.Server.Addr(),
		Handler:      r,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
		IdleTimeout:  cfg.Server.IdleTimeout,
	}

	go func() {
		log.Printf("server starting on %s", cfg.Server.Addr())
		if !cfg.Server.Loopback() {
			// The API speaks plain HTTP and has no certificate of its own, so
			// it must not be reachable from the network as it is. Say it out
			// loud at every start.
			log.Printf("WARNING: listening on %s, so the API is reachable from the network. "+
				"Put it behind a reverse proxy with TLS before you use it anywhere but this machine.",
				cfg.Server.Addr())
		}
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

// sweepOfflineVehicles marks the units that stopped reporting as offline,
// again and again, until the server stops.
//
// Every ten seconds is often enough: a unit goes offline at most that much
// later than it fell silent, and the check itself is one cheap UPDATE.
func sweepOfflineVehicles(h *handler.VehicleHandler) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		n, err := h.MarkStaleOffline(ctx)
		cancel()
		if err != nil {
			log.Printf("offline sweep failed: %v", err)
			continue
		}
		if n > 0 {
			log.Printf("offline sweep: %d unit(s) stopped reporting", n)
		}
	}
}

// sweepIdleVehicles opens an idle session for every unit that stood still
// long enough, again and again, until the server stops.
//
// Every thirty seconds is often enough: a unit turns idle at most that much
// later than it came to a stop, and the check itself is one cheap statement.
func sweepIdleVehicles(h *handler.VehicleHandler) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		n, err := h.MarkStationaryIdle(ctx)
		cancel()
		if err != nil {
			log.Printf("idle sweep failed: %v", err)
			continue
		}
		if n > 0 {
			log.Printf("idle sweep: %d unit(s) have been standing still", n)
		}
	}
}
