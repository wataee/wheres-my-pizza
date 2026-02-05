package kitchen

import (
	"context"
	"encoding/json"
	"fmt"
	"restaurant-system/internal/constants"
	"restaurant-system/internal/database"
	"restaurant-system/internal/logger"
	"restaurant-system/internal/models"
	"restaurant-system/internal/rabbitmq"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	amqp "github.com/rabbitmq/amqp091-go"
)

// Worker represents a kitchen worker
type Worker struct {
	DB                *pgxpool.Pool
	Amqp              *amqp.Channel
	WorkerName        string
	OrderTypes        []string
	HeartbeatInterval time.Duration
	processingCount   int // Current number of orders being processed
}

// NewWorker creates a new kitchen worker
func NewWorker(db *pgxpool.Pool, ch *amqp.Channel, name string, types string, heartbeatInterval int) *Worker {
	var typeList []string
	if types != "" {
		parts := strings.Split(types, ",")
		for _, p := range parts {
			trimmed := strings.TrimSpace(p)
			if trimmed != "" {
				typeList = append(typeList, trimmed)
			}
		}
	}

	return &Worker{
		DB:                db,
		Amqp:              ch,
		WorkerName:        name,
		OrderTypes:        typeList,
		HeartbeatInterval: time.Duration(heartbeatInterval) * time.Second,
	}
}

// Run starts the worker
func (w *Worker) Run(ctx context.Context, prefetch int) error {
	// Register worker in database
	if err := w.register(ctx); err != nil {
		logger.LogError("worker_registration_failed", "Failed to register worker", err, "startup")
		return err
	}
	defer w.setOffline()

	// Start heartbeat mechanism
	go w.startHeartbeat(ctx)

	// Setup RabbitMQ infrastructure
	if err := w.setupRabbitMQ(); err != nil {
		logger.LogError("startup", "Failed to setup RabbitMQ", err, "startup")
		return err
	}

	// Declare worker's queue with DLX
	queueName := fmt.Sprintf("kitchen_queue_%s", w.WorkerName)
	_, err := rabbitmq.DeclareQueueWithDLX(w.Amqp, queueName, 
		constants.ExchangeDLX, constants.RoutingKeyDeadLetter)
	if err != nil {
		logger.LogError("startup", "Failed to declare queue", err, "startup")
		return err
	}

	// Bind queue based on worker specialization
	if err := w.bindQueue(queueName); err != nil {
		logger.LogError("startup", "Failed to bind queue", err, "startup")
		return err
	}

	// Set QoS (prefetch count)
	if err := w.Amqp.Qos(prefetch, 0, false); err != nil {
		logger.LogError("startup", "Failed to set QoS", err, "startup")
		return err
	}

	// Start consuming messages
	msgs, err := w.Amqp.Consume(queueName, "", false, false, false, false, nil)
	if err != nil {
		logger.LogError("startup", "Failed to start consuming", err, "startup")
		return err
	}

	logger.LogInfo("service_started", "Worker started listening", "startup",
		"worker", w.WorkerName,
		"types", strings.Join(w.OrderTypes, ","),
		"prefetch", prefetch)

	// Process messages
	for {
		select {
		case <-ctx.Done():
			logger.LogInfo("graceful_shutdown", "Stopping worker...", "shutdown",
				"worker", w.WorkerName)
			return nil
		case d, ok := <-msgs:
			if !ok {
				logger.LogInfo("graceful_shutdown", "Channel closed", "shutdown")
				return nil
			}
			w.processMessage(ctx, d)
		}
	}
}

// setupRabbitMQ sets up RabbitMQ exchanges and queues
func (w *Worker) setupRabbitMQ() error {
	// Declare notifications exchange
	if err := rabbitmq.DeclareExchange(w.Amqp, 
		constants.ExchangeNotificationsFanout, "fanout"); err != nil {
		return fmt.Errorf("failed to declare notifications exchange: %w", err)
	}

	// Declare Dead Letter Exchange
	if err := rabbitmq.DeclareExchange(w.Amqp, 
		constants.ExchangeDLX, "direct"); err != nil {
		return fmt.Errorf("failed to declare DLX: %w", err)
	}

	// Declare Dead Letter Queue
	_, err := rabbitmq.DeclareQueue(w.Amqp, constants.QueueDeadLetter, nil)
	if err != nil {
		return fmt.Errorf("failed to declare DLQ: %w", err)
	}

	// Bind DLQ to DLX
	if err := rabbitmq.BindQueue(w.Amqp, constants.QueueDeadLetter, 
		constants.RoutingKeyDeadLetter, constants.ExchangeDLX); err != nil {
		return fmt.Errorf("failed to bind DLQ: %w", err)
	}

	return nil
}

// bindQueue binds the worker's queue to the orders exchange
func (w *Worker) bindQueue(queueName string) error {
	routingKeys := []string{fmt.Sprintf("%s.#", constants.RoutingKeyKitchen)}

	// If worker is specialized, bind only to specific order types
	if len(w.OrderTypes) > 0 {
		routingKeys = []string{}
		for _, orderType := range w.OrderTypes {
			// Bind to all priorities for this order type
			for priority := constants.PriorityLow; priority <= constants.PriorityHigh; priority++ {
				key := fmt.Sprintf("%s.%s.%d", 
					constants.RoutingKeyKitchen, orderType, priority)
				routingKeys = append(routingKeys, key)
			}
		}
	}

	// Bind queue to exchange with routing keys
	for _, key := range routingKeys {
		if err := rabbitmq.BindQueue(w.Amqp, queueName, key, 
			constants.ExchangeOrdersTopic); err != nil {
			return fmt.Errorf("failed to bind queue with key %s: %w", key, err)
		}
	}

	logger.LogDebug("queue_bound", "Queue bound to exchange", "startup",
		"queue", queueName,
		"routing_keys", strings.Join(routingKeys, ","))

	return nil
}

// processMessage processes a single order message
func (w *Worker) processMessage(ctx context.Context, d amqp.Delivery) {
	var order models.OrderMessage
	
	// Parse message
	if err := json.Unmarshal(d.Body, &order); err != nil {
		logger.LogError("message_processing_failed", "Invalid JSON in message", err, "")
		// Invalid JSON - send to DLQ (will never be valid)
		d.Nack(false, false)
		return
	}

	// Check worker specialization
	if !w.canHandleOrderType(order.OrderType) {
		logger.LogDebug("message_requeued", "Order type not handled by this worker", "",
			"order_number", order.OrderNumber,
			"order_type", order.OrderType,
			"worker_types", strings.Join(w.OrderTypes, ","))
		// Not my job - requeue for another worker
		d.Nack(false, true)
		return
	}

	logger.LogDebug("order_processing_started", "Processing order", order.OrderNumber,
		"order_number", order.OrderNumber,
		"customer", order.CustomerName,
		"type", order.OrderType,
		"priority", order.Priority)

	// Process order: received -> cooking
	if err := w.startCooking(ctx, order); err != nil {
		logger.LogError("message_processing_failed", "Failed to start cooking", err, order.OrderNumber)
		// Temporary error - requeue
		d.Nack(false, true)
		return
	}

	// Publish status update: cooking
	estimatedCompletion := time.Now().UTC().Add(w.getCookingTime(order.OrderType))
	w.publishNotification(ctx, order.OrderNumber, 
		constants.OrderStatusReceived, constants.OrderStatusCooking, estimatedCompletion)

	// Simulate cooking
	cookingTime := w.getCookingTime(order.OrderType)
	select {
	case <-ctx.Done():
		// Shutdown requested - requeue message
		logger.LogInfo("graceful_shutdown", "Requeuing message due to shutdown", order.OrderNumber)
		d.Nack(false, true)
		return
	case <-time.After(cookingTime):
		// Cooking complete
	}

	// Process order: cooking -> ready
	if err := w.finishCooking(ctx, order); err != nil {
		logger.LogError("message_processing_failed", "Failed to finish cooking", err, order.OrderNumber)
		// Temporary error - requeue
		d.Nack(false, true)
		return
	}

	// Publish status update: ready
	w.publishNotification(ctx, order.OrderNumber, 
		constants.OrderStatusCooking, constants.OrderStatusReady, time.Now().UTC())

	logger.LogDebug("order_completed", "Order ready", order.OrderNumber,
		"order_number", order.OrderNumber,
		"cooking_time", cookingTime.Seconds())

	// Acknowledge message - successfully processed
	if err := d.Ack(false); err != nil {
		logger.LogError("message_ack_failed", "Failed to acknowledge message", err, order.OrderNumber)
	}
}

// canHandleOrderType checks if worker can handle the order type
func (w *Worker) canHandleOrderType(orderType string) bool {
	// If no specialization, can handle all types
	if len(w.OrderTypes) == 0 {
		return true
	}

	// Check if order type matches worker's specialization
	for _, t := range w.OrderTypes {
		if t == orderType {
			return true
		}
	}

	return false
}

// startCooking updates order status to cooking
func (w *Worker) startCooking(ctx context.Context, order models.OrderMessage) error {
	return database.RunInTxWithRetry(ctx, w.DB, func(tx pgx.Tx) error {
		// Lock order row and check current status (idempotency)
		var currentStatus string
		err := tx.QueryRow(ctx, 
			"SELECT status FROM orders WHERE number = $1 FOR UPDATE", 
			order.OrderNumber).Scan(&currentStatus)
		if err != nil {
			return fmt.Errorf("failed to select order: %w", err)
		}

		// Idempotency check - order already processed
		if currentStatus != constants.OrderStatusReceived {
			logger.LogDebug("order_already_processed", "Order already in processing", order.OrderNumber,
				"current_status", currentStatus)
			return fmt.Errorf("order already processed (status: %s)", currentStatus)
		}

		// Update order status to cooking
		_, err = tx.Exec(ctx, `
			UPDATE orders 
			SET status = $1, processed_by = $2, updated_at = NOW() 
			WHERE number = $3
		`, constants.OrderStatusCooking, w.WorkerName, order.OrderNumber)
		if err != nil {
			return fmt.Errorf("failed to update order status: %w", err)
		}

		// Log status change
		_, err = tx.Exec(ctx, `
			INSERT INTO order_status_log (order_id, status, changed_by)
			SELECT id, $1, $2 FROM orders WHERE number = $3
		`, constants.OrderStatusCooking, w.WorkerName, order.OrderNumber)
		if err != nil {
			return fmt.Errorf("failed to log status change: %w", err)
		}

		return nil
	})
}

// finishCooking updates order status to ready
func (w *Worker) finishCooking(ctx context.Context, order models.OrderMessage) error {
	return database.RunInTxWithRetry(ctx, w.DB, func(tx pgx.Tx) error {
		// Update order status to ready
		_, err := tx.Exec(ctx, `
			UPDATE orders 
			SET status = $1, completed_at = NOW(), updated_at = NOW() 
			WHERE number = $2
		`, constants.OrderStatusReady, order.OrderNumber)
		if err != nil {
			return fmt.Errorf("failed to update order status: %w", err)
		}

		// Log status change
		_, err = tx.Exec(ctx, `
			INSERT INTO order_status_log (order_id, status, changed_by)
			SELECT id, $1, $2 FROM orders WHERE number = $3
		`, constants.OrderStatusReady, w.WorkerName, order.OrderNumber)
		if err != nil {
			return fmt.Errorf("failed to log status change: %w", err)
		}

		// Increment worker's processed count
		_, err = tx.Exec(ctx, `
			UPDATE workers 
			SET orders_processed = orders_processed + 1 
			WHERE name = $1
		`, w.WorkerName)
		if err != nil {
			return fmt.Errorf("failed to update worker stats: %w", err)
		}

		return nil
	})
}

// publishNotification publishes a status update notification
func (w *Worker) publishNotification(ctx context.Context, orderNum, oldStatus, newStatus string, estimatedCompletion time.Time) {
	msg := models.OrderStatusUpdate{
		OrderNumber:         orderNum,
		OldStatus:           oldStatus,
		NewStatus:           newStatus,
		ChangedBy:           w.WorkerName,
		Timestamp:           time.Now().UTC(),
		EstimatedCompletion: estimatedCompletion,
	}

	body, err := json.Marshal(msg)
	if err != nil {
		logger.LogError("notification_marshal_failed", "Failed to marshal notification", err, orderNum)
		return
	}

	publishing := amqp.Publishing{
		ContentType: "application/json",
		Body:        body,
		Timestamp:   time.Now().UTC(),
	}

	pubCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	err = w.Amqp.PublishWithContext(pubCtx, 
		constants.ExchangeNotificationsFanout, "", false, false, publishing)
	if err != nil {
		logger.LogError("notification_publish_failed", "Failed to publish notification", err, orderNum)
	}
}

// getCookingTime returns cooking time based on order type
func (w *Worker) getCookingTime(orderType string) time.Duration {
	switch orderType {
	case constants.OrderTypeDineIn:
		return constants.CookingTimeDineIn
	case constants.OrderTypeTakeout:
		return constants.CookingTimeTakeout
	case constants.OrderTypeDelivery:
		return constants.CookingTimeDelivery
	default:
		return constants.CookingTimeTakeout
	}
}

// register registers the worker in the database
func (w *Worker) register(ctx context.Context) error {
	regCtx, cancel := context.WithTimeout(ctx, constants.ContextTimeoutDB)
	defer cancel()

	var status string
	err := w.DB.QueryRow(regCtx, 
		"SELECT status FROM workers WHERE name = $1", w.WorkerName).Scan(&status)

	if err == pgx.ErrNoRows {
		// New worker - insert
		_, err = w.DB.Exec(regCtx, `
			INSERT INTO workers (name, type, status)
			VALUES ($1, $2, $3)
		`, w.WorkerName, "general", constants.WorkerStatusOnline)
	} else if err == nil {
		// Existing worker
		if status == constants.WorkerStatusOnline {
			// Duplicate worker detected
			return fmt.Errorf("worker %s is already online", w.WorkerName)
		}
		// Offline worker - bring back online
		_, err = w.DB.Exec(regCtx, `
			UPDATE workers 
			SET status = $1, last_seen = NOW() 
			WHERE name = $2
		`, constants.WorkerStatusOnline, w.WorkerName)
	}

	if err != nil {
		return err
	}

	logger.LogInfo("worker_registered", "Worker registered successfully", "startup",
		"name", w.WorkerName,
		"types", strings.Join(w.OrderTypes, ","))
	
	return nil
}

// setOffline marks the worker as offline
func (w *Worker) setOffline() {
	ctx, cancel := context.WithTimeout(context.Background(), constants.ContextTimeoutDB)
	defer cancel()

	_, err := w.DB.Exec(ctx, `
		UPDATE workers SET status = $1 WHERE name = $2
	`, constants.WorkerStatusOffline, w.WorkerName)
	
	if err != nil {
		logger.LogError("worker_offline_failed", "Failed to set worker offline", err, "shutdown")
	} else {
		logger.LogInfo("graceful_shutdown", "Worker set to offline", "shutdown", 
			"worker", w.WorkerName)
	}
}

// startHeartbeat sends periodic heartbeats
func (w *Worker) startHeartbeat(ctx context.Context) {
	ticker := time.NewTicker(w.HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			hbCtx, cancel := context.WithTimeout(ctx, constants.ContextTimeoutDB)
			_, err := w.DB.Exec(hbCtx, `
				UPDATE workers 
				SET last_seen = NOW(), status = $1 
				WHERE name = $2
			`, constants.WorkerStatusOnline, w.WorkerName)
			cancel()

			if err != nil {
				logger.LogError("heartbeat_failed", "Failed to send heartbeat", err, "heartbeat")
			} else {
				logger.LogDebug("heartbeat_sent", "Heartbeat sent", "heartbeat",
					"worker", w.WorkerName)
			}
		}
	}
}
