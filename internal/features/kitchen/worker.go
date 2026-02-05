package kitchen

import (
	"context"
	"encoding/json"
	"fmt"
	"restaurant-system/internal/database"
	"restaurant-system/internal/logger"
	"restaurant-system/internal/models"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	amqp "github.com/rabbitmq/amqp091-go"
)

type Worker struct {
	DB         *pgxpool.Pool
	Amqp       *amqp.Channel
	WorkerName string
	OrderTypes []string
}

func NewWorker(db *pgxpool.Pool, ch *amqp.Channel, name string, types string) *Worker {
	var typeList []string
	if types != "" {
		typeList = strings.Split(types, ",")
	}
	return &Worker{
		DB:         db,
		Amqp:       ch,
		WorkerName: name,
		OrderTypes: typeList,
	}
}

func (w *Worker) Run(ctx context.Context, prefetch int) error {
	if err := w.register(); err != nil {
		return err
	}
	defer w.setOffline()


	go w.startHeartbeat(ctx)

	err := w.Amqp.ExchangeDeclare("notifications_fanout", "fanout", true, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("declare fanout failed: %w", err)
	}

	queueName := fmt.Sprintf("kitchen_queue_%s", w.WorkerName)
	_, err = w.Amqp.QueueDeclare(queueName, true, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("declare queue failed: %w", err)
	}

	routingKeys := []string{"kitchen.#"} 
	if len(w.OrderTypes) > 0 {
		routingKeys = []string{}
		for _, t := range w.OrderTypes {
			routingKeys = append(routingKeys, fmt.Sprintf("kitchen.%s.*", strings.TrimSpace(t)))
		}
	}

	for _, key := range routingKeys {
		if err := w.Amqp.QueueBind(queueName, key, "orders_topic", false, nil); err != nil {
			return fmt.Errorf("bind failed: %w", err)
		}
	}

	if err := w.Amqp.Qos(prefetch, 0, false); err != nil {
		return fmt.Errorf("qos failed: %w", err)
	}

	msgs, err := w.Amqp.Consume(queueName, "", false, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("consume failed: %w", err)
	}

	logger.LogInfo("service_started", "Worker started listening", "", "worker", w.WorkerName, "types", routingKeys)

	for {
		select {
		case <-ctx.Done():
			logger.LogInfo("graceful_shutdown", "Stopping worker...", "")
			return nil
		case d, ok := <-msgs:
			if !ok {
				return nil
			}
			w.processMessage(ctx, d)
		}
	}
}

func (w *Worker) processMessage(ctx context.Context, d amqp.Delivery) {
	var order models.OrderMessage
	if err := json.Unmarshal(d.Body, &order); err != nil {
		logger.LogError("message_processing_failed", "Invalid JSON", err, "")
		d.Nack(false, false)
		return
	}

	logger.LogInfo("order_processing_started", "Processing order", "", "order_number", order.OrderNumber)


	err := database.RunInTx(ctx, w.DB, func(tx pgx.Tx) error {
		var currentStatus string
		err := tx.QueryRow(ctx, "SELECT status FROM orders WHERE number = $1 FOR UPDATE", order.OrderNumber).Scan(&currentStatus)
		if err != nil {
			return err
		}
		if currentStatus != "received" {
			return fmt.Errorf("order already processed or cancelled")
		}

		_, err = tx.Exec(ctx, "UPDATE orders SET status = 'cooking', processed_by = $1, updated_at = NOW() WHERE number = $2", w.WorkerName, order.OrderNumber)
		if err != nil {
			return err
		}

		_, err = tx.Exec(ctx, "INSERT INTO order_status_log (order_id, status, changed_by) SELECT id, 'cooking', $1 FROM orders WHERE number = $2", w.WorkerName, order.OrderNumber)
		return err
	})

	if err != nil {
		logger.LogError("message_processing_failed", "DB Error on start cooking", err, order.OrderNumber)
		d.Nack(false, true) 
		return
	}

	w.publishNotification(ctx, order.OrderNumber, "received", "cooking")

	sleepTime := 8 * time.Second
	if order.OrderType == "takeout" {
		sleepTime = 10 * time.Second
	} else if order.OrderType == "delivery" {
		sleepTime = 12 * time.Second
	}

	select {
	case <-ctx.Done():
		d.Nack(false, true)
		return
	case <-time.After(sleepTime):
	}

	err = database.RunInTx(ctx, w.DB, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "UPDATE orders SET status = 'ready', completed_at = NOW(), updated_at = NOW() WHERE number = $1", order.OrderNumber)
		if err != nil {
			return err
		}

		_, err = tx.Exec(ctx, "INSERT INTO order_status_log (order_id, status, changed_by) SELECT id, 'ready', $1 FROM orders WHERE number = $2", w.WorkerName, order.OrderNumber)
		if err != nil {
			return err
		}

		_, err = tx.Exec(ctx, "UPDATE workers SET orders_processed = orders_processed + 1 WHERE name = $1", w.WorkerName)
		return err
	})

	if err != nil {
		logger.LogError("message_processing_failed", "DB Error on finish cooking", err, order.OrderNumber)
		d.Nack(false, true)
		return
	}

	w.publishNotification(ctx, order.OrderNumber, "cooking", "ready")
	logger.LogInfo("order_completed", "Order ready", "", "order_number", order.OrderNumber)

	d.Ack(false)
}

func (w *Worker) publishNotification(ctx context.Context, orderNum, oldStatus, newStatus string) {
	msg := models.OrderStatusUpdate{
		OrderNumber: orderNum,
		OldStatus:   oldStatus,
		NewStatus:   newStatus,
		ChangedBy:   w.WorkerName,
		Timestamp:   time.Now(),
	}
	body, _ := json.Marshal(msg)
	
	_ = w.Amqp.PublishWithContext(ctx, "notifications_fanout", "", false, false, amqp.Publishing{
		ContentType: "application/json",
		Body:        body,
	})
}

func (w *Worker) register() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()


	var status string
	err := w.DB.QueryRow(ctx, "SELECT status FROM workers WHERE name = $1", w.WorkerName).Scan(&status)
	
	if err == pgx.ErrNoRows {
	
		_, err = w.DB.Exec(ctx, "INSERT INTO workers (name, type, status) VALUES ($1, 'general', 'online')", w.WorkerName)
	} else if err == nil {
		
		if status == "online" {
			return fmt.Errorf("worker_registration_failed: worker %s is already online", w.WorkerName)
		}
		_, err = w.DB.Exec(ctx, "UPDATE workers SET status = 'online', last_seen = NOW() WHERE name = $1", w.WorkerName)
	}

	if err != nil {
		logger.LogError("worker_registration_failed", "DB error", err, "")
		return err
	}
	
	logger.LogInfo("worker_registered", "Worker registered successfully", "", "name", w.WorkerName)
	return nil
}

func (w *Worker) setOffline() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	w.DB.Exec(ctx, "UPDATE workers SET status = 'offline' WHERE name = $1", w.WorkerName)
}

func (w *Worker) startHeartbeat(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, err := w.DB.Exec(ctx, "UPDATE workers SET last_seen = NOW(), status = 'online' WHERE name = $1", w.WorkerName)
			if err != nil {
				logger.LogError("heartbeat_failed", "Failed to send heartbeat", err, "")
			} else {
				logger.LogInfo("heartbeat_sent", "Heartbeat sent", "", "worker", w.WorkerName)
			}
		}
	}
}