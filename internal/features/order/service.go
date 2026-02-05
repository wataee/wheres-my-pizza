package order

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"restaurant-system/internal/database"
	"restaurant-system/internal/logger"
	"restaurant-system/internal/models"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	amqp "github.com/rabbitmq/amqp091-go"
)

type Service struct {
	DB   *pgxpool.Pool
	Amqp *amqp.Channel
}

func NewService(db *pgxpool.Pool, ch *amqp.Channel) *Service {
	return &Service{
		DB:   db,
		Amqp: ch,
	}
}

func (s *Service) CreateOrder(ctx context.Context, req models.OrderRequest) (*models.OrderResponse, error) {
	if err := validateOrder(req); err != nil {
		return nil, err
	}

	totalAmount := 0.0
	for _, item := range req.Items {
		totalAmount += item.Price * float64(item.Quantity)
	}

	priority := 1
	if totalAmount > 100 {
		priority = 10
	} else if totalAmount > 50 {
		priority = 5
	}

	var orderNumber string
	var orderID int

	err := database.RunInTx(ctx, s.DB, func(tx pgx.Tx) error {
		dateStr := time.Now().Format("2006-01-02")
		var counter int
		err := tx.QueryRow(ctx, `
			INSERT INTO daily_order_counters (date, counter)
			VALUES ($1, 1)
			ON CONFLICT (date) DO UPDATE 
			SET counter = daily_order_counters.counter + 1
			RETURNING counter
		`, dateStr).Scan(&counter)
		if err != nil {
			return err
		}

		orderNumber = fmt.Sprintf("ORD_%s_%03d", time.Now().Format("20060102"), counter)

		err = tx.QueryRow(ctx, `
			INSERT INTO orders (number, customer_name, type, table_number, delivery_address, total_amount, priority, status)
			VALUES ($1, $2, $3, $4, $5, $6, $7, 'received')
			RETURNING id
		`, orderNumber, req.CustomerName, req.OrderType, req.TableNumber, req.DeliveryAddress, totalAmount, priority).Scan(&orderID)
		if err != nil {
			return err
		}

		for _, item := range req.Items {
			_, err := tx.Exec(ctx, `
				INSERT INTO order_items (order_id, name, quantity, price)
				VALUES ($1, $2, $3, $4)
			`, orderID, item.Name, item.Quantity, item.Price)
			if err != nil {
				return err
			}
		}

		_, err = tx.Exec(ctx, `
			INSERT INTO order_status_log (order_id, status, changed_by)
			VALUES ($1, 'received', 'order-service')
		`, orderID)
		
		return err
	})

	if err != nil {
		logger.LogError("db_transaction_failed", "Failed to store order", err, "")
		return nil, err
	}

	msg := models.OrderMessage{
		OrderNumber:     orderNumber,
		CustomerName:    req.CustomerName,
		OrderType:       req.OrderType,
		TableNumber:     req.TableNumber,
		DeliveryAddress: req.DeliveryAddress,
		Items:           req.Items,
		TotalAmount:     totalAmount,
		Priority:        priority,
	}

	body, _ := json.Marshal(msg)
	routingKey := fmt.Sprintf("kitchen.%s.%d", req.OrderType, priority)

	err = s.Amqp.PublishWithContext(ctx, "orders_topic", routingKey, false, false, amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent,
		Priority:     uint8(priority),
		Body:         body,
	})

	if err != nil {
		logger.LogError("rabbitmq_publish_failed", "Failed to publish order", err, orderNumber)
		return nil, errors.New("internal system error")
	}

	logger.LogInfo("order_published", "Order published to RabbitMQ", "", "order_number", orderNumber, "routing_key", routingKey)

	return &models.OrderResponse{
		OrderNumber: orderNumber,
		Status:      "received",
		TotalAmount: totalAmount,
	}, nil
}

func validateOrder(req models.OrderRequest) error {
	if len(req.CustomerName) == 0 || len(req.CustomerName) > 100 {
		return errors.New("invalid customer_name")
	}
	if req.OrderType != "dine_in" && req.OrderType != "takeout" && req.OrderType != "delivery" {
		return errors.New("invalid order_type")
	}
	if len(req.Items) < 1 || len(req.Items) > 20 {
		return errors.New("items count must be between 1 and 20")
	}

	for _, item := range req.Items {
		if len(item.Name) == 0 || len(item.Name) > 50 {
			return errors.New("invalid item name")
		}
		if item.Quantity < 1 || item.Quantity > 10 {
			return errors.New("invalid item quantity")
		}
		if item.Price < 0.01 || item.Price > 999.99 {
			return errors.New("invalid item price")
		}
	}

	if req.OrderType == "dine_in" {
		if req.TableNumber == nil || *req.TableNumber < 1 || *req.TableNumber > 100 {
			return errors.New("invalid table_number for dine_in")
		}
	}

	if req.OrderType == "delivery" {
		if len(req.DeliveryAddress) < 10 {
			return errors.New("delivery_address too short")
		}
	}

	return nil
}