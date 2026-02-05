package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"restaurant-system/internal/config"
	"restaurant-system/internal/constants"
	"restaurant-system/internal/database"
	"restaurant-system/internal/features/kitchen"
	"restaurant-system/internal/features/notification"
	"restaurant-system/internal/features/order"
	"restaurant-system/internal/features/tracking"
	"restaurant-system/internal/logger"
	"restaurant-system/internal/rabbitmq"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	// Parse command line flags
	mode := flag.String("mode", "", "Service mode")
	port := flag.Int("port", constants.DefaultOrderServicePort, "HTTP port")
	workerName := flag.String("worker-name", "", "Worker name")
	orderTypes := flag.String("order-types", "", "Order types (comma-separated)")
	prefetch := flag.Int("prefetch", constants.DefaultPrefetchCount, "Prefetch count")
	heartbeatInterval := flag.Int("heartbeat-interval", int(constants.DefaultHeartbeatInterval.Seconds()), "Heartbeat interval in seconds")
	configPath := flag.String("config", "config.yaml", "Config file path")

	flag.Parse()

	// Validate required flags
	if *mode == "" {
		fmt.Println("Error: --mode is required")
		os.Exit(1)
	}

	// Initialize logger
	logger.Init(*mode)
	logger.LogInfo("startup", fmt.Sprintf("Starting %s", *mode), "startup")

	// Load configuration
	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		logger.LogError("startup", "Failed to load config", err, "startup")
		os.Exit(1)
	}

	// Connect to database
	dbPool, err := database.Connect(context.Background(), cfg.DBConnectionURL())
	if err != nil {
		logger.LogError("startup", "DB connection failed", err, "startup")
		os.Exit(1)
	}
	defer dbPool.Close()

	// Connect to RabbitMQ
	rmqClient, err := rabbitmq.Connect(cfg.RabbitMQURL())
	if err != nil {
		logger.LogError("startup", "RabbitMQ connection failed", err, "startup")
		os.Exit(1)
	}
	defer rmqClient.Close()

	// Start appropriate service based on mode
	switch *mode {
	case constants.ServiceOrderService:
		runOrderService(dbPool, rmqClient, *port)
	case constants.ServiceKitchenWorker:
		if *workerName == "" {
			logger.LogError("startup", "--worker-name is required for kitchen-worker mode",
				fmt.Errorf("missing required flag"), "startup")
			os.Exit(1)
		}
		runKitchenWorker(dbPool, rmqClient, *workerName, *orderTypes, *prefetch, *heartbeatInterval)
	case constants.ServiceTrackingService:
		runTrackingService(dbPool, *port)
	case constants.ServiceNotificationSubscriber:
		runNotificationSubscriber(rmqClient)
	default:
		logger.LogError("startup", "Unknown mode: "+*mode, fmt.Errorf("invalid mode"), "startup")
		os.Exit(1)
	}
}

// runOrderService starts the order service
func runOrderService(db *pgxpool.Pool, rmq *rabbitmq.Client, port int) {
	ch, err := rmq.CreateChannelWithRetry(constants.MaxRetryAttempts)
	if err != nil {
		logger.LogError("startup", "Failed to create channel", err, "startup")
		os.Exit(1)
	}
	defer ch.Close()

	// Declare orders exchange
	if err := rabbitmq.DeclareExchange(ch, constants.ExchangeOrdersTopic, "topic"); err != nil {
		logger.LogError("startup", "Failed to declare exchange", err, "startup")
		os.Exit(1)
	}

	logger.LogInfo("rabbitmq_connected", 
		fmt.Sprintf("Connected to RabbitMQ exchange '%s'", constants.ExchangeOrdersTopic), "startup")

	// Create service and handler
	svc := order.NewService(db, ch)
	handler := order.NewHandler(svc, db)

	// Setup HTTP routes
	mux := http.NewServeMux()
	mux.HandleFunc("/orders", handler.CreateOrder)
	mux.HandleFunc("/health", handler.HealthCheck)

	startHTTPServer(port, mux, constants.ServiceOrderService)
}

// runKitchenWorker starts a kitchen worker
func runKitchenWorker(db *pgxpool.Pool, rmq *rabbitmq.Client, name, types string, prefetch int, heartbeatInterval int) {
	ch, err := rmq.CreateChannelWithRetry(constants.MaxRetryAttempts)
	if err != nil {
		logger.LogError("startup", "Failed to create channel", err, "startup")
		os.Exit(1)
	}
	defer ch.Close()

	// Declare orders exchange
	if err := rabbitmq.DeclareExchange(ch, constants.ExchangeOrdersTopic, "topic"); err != nil {
		logger.LogError("startup", "Failed to declare orders_topic exchange", err, "startup")
		os.Exit(1)
	}

	// Create worker
	worker := kitchen.NewWorker(db, ch, name, types, heartbeatInterval)

	// Setup graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		logger.LogInfo("shutdown", fmt.Sprintf("Received signal: %v", sig), "shutdown")
		cancel()
	}()

	// Run worker
	if err := worker.Run(ctx, prefetch); err != nil {
		logger.LogError("worker_error", "Worker stopped with error", err, "shutdown")
		os.Exit(1)
	}

	logger.LogInfo("shutdown", "Worker stopped gracefully", "shutdown")
}

// runTrackingService starts the tracking service
func runTrackingService(db *pgxpool.Pool, port int) {
	svc := tracking.NewService(db)
	handler := tracking.NewHandler(svc, db)

	// Setup HTTP routes
	mux := http.NewServeMux()

	mux.HandleFunc("/orders/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/status") {
			handler.GetOrderStatus(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/history") {
			handler.GetOrderHistory(w, r)
			return
		}
		http.NotFound(w, r)
	})

	mux.HandleFunc("/workers/status", handler.GetWorkersStatus)
	mux.HandleFunc("/health", handler.HealthCheck)

	startHTTPServer(port, mux, constants.ServiceTrackingService)
}

// runNotificationSubscriber starts the notification subscriber
func runNotificationSubscriber(rmq *rabbitmq.Client) {
	ch, err := rmq.CreateChannelWithRetry(constants.MaxRetryAttempts)
	if err != nil {
		logger.LogError("startup", "Failed to create channel", err, "startup")
		os.Exit(1)
	}
	defer ch.Close()

	sub := notification.NewSubscriber(ch)

	// Setup graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		logger.LogInfo("shutdown", fmt.Sprintf("Received signal: %v", sig), "shutdown")
		cancel()
	}()

	// Run subscriber
	if err := sub.Run(ctx); err != nil {
		logger.LogError("subscriber_error", "Subscriber stopped with error", err, "shutdown")
		os.Exit(1)
	}

	logger.LogInfo("shutdown", "Subscriber stopped gracefully", "shutdown")
}

// startHTTPServer starts an HTTP server with graceful shutdown
func startHTTPServer(port int, handler http.Handler, serviceName string) {
	server := &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		Handler:           handler,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Start server in background
	go func() {
		logger.LogInfo("service_started",
			fmt.Sprintf("%s started on port %d", serviceName, port), "startup",
			"port", port,
			"service", serviceName)

		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.LogError("server_error", "HTTP server error", err, "shutdown")
		}
	}()

	// Wait for shutdown signal
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	// Graceful shutdown
	logger.LogInfo("graceful_shutdown", "Shutting down HTTP server gracefully...", "shutdown")
	ctx, cancel := context.WithTimeout(context.Background(), constants.ContextTimeoutShutdown)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		logger.LogError("shutdown_error", "Server forced to shutdown", err, "shutdown")
	} else {
		logger.LogInfo("shutdown", "Server stopped gracefully", "shutdown")
	}
}
