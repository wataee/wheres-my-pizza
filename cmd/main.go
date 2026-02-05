package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"restaurant-system/internal/config"
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
	mode := flag.String("mode", "", "Service mode")
	port := flag.Int("port", 3000, "HTTP port")
	workerName := flag.String("worker-name", "", "Worker name")
	orderTypes := flag.String("order-types", "", "Order types")
	prefetch := flag.Int("prefetch", 1, "Prefetch count")

	flag.Parse()

	if *mode == "" {
		fmt.Println("Error: --mode is required")
		os.Exit(1)
	}

	logger.Init(*mode)

	cfg, err := config.LoadConfig("config.yaml")
	if err != nil {
		logger.LogError("startup", "Failed to load config", err, "")
		os.Exit(1)
	}

	dbPool, err := database.Connect(context.Background(), cfg.DBConnectionURL())
	if err != nil {
		logger.LogError("startup", "DB connection failed", err, "")
		os.Exit(1)
	}
	defer dbPool.Close()

	rmqClient, err := rabbitmq.Connect(cfg.RabbitMQURL())
	if err != nil {
		logger.LogError("startup", "RabbitMQ connection failed", err, "")
		os.Exit(1)
	}
	defer rmqClient.Close()

	switch *mode {
	case "order-service":
		runOrderService(dbPool, rmqClient, *port)
	case "kitchen-worker":
		if *workerName == "" {
			logger.LogError("startup", "--worker-name is required", nil, "")
			os.Exit(1)
		}
		runKitchenWorker(dbPool, rmqClient, *workerName, *orderTypes, *prefetch)
	case "tracking-service":
		runTrackingService(dbPool, *port)
	case "notification-subscriber":
		runNotificationSubscriber(rmqClient)
	default:
		logger.LogError("startup", "Unknown mode: "+*mode, nil, "")
		os.Exit(1)
	}
}

func runOrderService(db *pgxpool.Pool, rmq *rabbitmq.Client, port int) {
	ch, err := rmq.CreateChannel()
	if err != nil {
		logger.LogError("startup", "Failed to create channel", err, "")
		os.Exit(1)
	}
	defer ch.Close()

	err = ch.ExchangeDeclare("orders_topic", "topic", true, false, false, false, nil)
	if err != nil {
		logger.LogError("startup", "Failed to declare exchange", err, "")
		os.Exit(1)
	}

	svc := order.NewService(db, ch)
	handler := order.NewHandler(svc)

	mux := http.NewServeMux()
	mux.HandleFunc("/orders", handler.CreateOrder)

	startHTTPServer(port, mux)
}

func runKitchenWorker(db *pgxpool.Pool, rmq *rabbitmq.Client, name, types string, prefetch int) {
	ch, err := rmq.CreateChannel()
	if err != nil {
		logger.LogError("startup", "Failed to create channel", err, "")
		os.Exit(1)
	}
	defer ch.Close()

	worker := kitchen.NewWorker(db, ch, name, types)

	ctx, cancel := context.WithCancel(context.Background())
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		logger.LogInfo("shutdown", "Shutdown signal received", "")
		cancel()
	}()

	if err := worker.Run(ctx, prefetch); err != nil {
		logger.LogError("worker_error", "Worker stopped with error", err, "")
		os.Exit(1)
	}
}

func runTrackingService(db *pgxpool.Pool, port int) {
	svc := tracking.NewService(db)
	handler := tracking.NewHandler(svc)

	mux := http.NewServeMux()
	
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/status") && strings.HasPrefix(r.URL.Path, "/orders/") {
			handler.GetOrderStatus(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/history") && strings.HasPrefix(r.URL.Path, "/orders/") {
			handler.GetOrderHistory(w, r)
			return
		}
		if r.URL.Path == "/workers/status" {
			handler.GetWorkersStatus(w, r)
			return
		}
		http.NotFound(w, r)
	})

	startHTTPServer(port, mux)
}

func runNotificationSubscriber(rmq *rabbitmq.Client) {
	ch, err := rmq.CreateChannel()
	if err != nil {
		logger.LogError("startup", "Failed to create channel", err, "")
		os.Exit(1)
	}
	defer ch.Close()

	sub := notification.NewSubscriber(ch)

	ctx, cancel := context.WithCancel(context.Background())
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		logger.LogInfo("shutdown", "Shutdown signal received", "")
		cancel()
	}()

	if err := sub.Run(ctx); err != nil {
		logger.LogError("subscriber_error", "Subscriber stopped with error", err, "")
		os.Exit(1)
	}
}

func startHTTPServer(port int, handler http.Handler) {
	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: handler,
	}

	go func() {
		logger.LogInfo("service_started", fmt.Sprintf("Service started on port %d", port), "")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.LogError("shutdown", "Server error", err, "")
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	logger.LogInfo("shutdown", "Shutting down gracefully...", "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server.Shutdown(ctx)
}