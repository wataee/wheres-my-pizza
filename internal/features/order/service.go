package order

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"restaurant-system/internal/constants"
	"restaurant-system/internal/database"
	"restaurant-system/internal/logger"
	"restaurant-system/internal/models"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	amqp "github.com/rabbitmq/amqp091-go"
)

var (
	// customerNameRegex validates customer name format
	customerNameRegex = regexp.MustCompile(`^[a-zA-Z0-9\s\-']+$`)
)

// Service handles order creation and management
type Service struct {
	DB   *pgxpool.Pool
	Amqp *amqp.Channel
}

// NewService creates a new order service
func NewService(db *pgxpool.Pool, ch *amqp.Channel) *Service {
	return &Service{
		DB:   db,
		Amqp: ch,
	}
}

// CreateOrder creates a new customer order
func (s *Service) CreateOrder(ctx context.Context, req models.OrderRequest, requestID string) (*models.OrderResponse, error) {
	// Add timeout to context
	ctx, cancel := context.WithTimeout(ctx, constants.ContextTimeoutDefault)
	defer cancel()

	// Validate order
	if err := s.validateOrder(req); err != nil {
		logger.LogError("validation_failed", "Order validation failed", err, requestID)
		return nil, err
	}

	logger.LogDebug("order_received", "New order received", requestID,
		"customer", req.CustomerName,
		"type", req.OrderType,
		"items_count", len(req.Items))

	// Calculate total amount
	totalAmount := calculateTotalAmount(req.Items)

	// Determine priority based on total amount
	priority := calculatePriority(totalAmount)

	var orderNumber string
	var orderID int

	// Execute in transaction with retry
	err := database.RunInTxWithRetry(ctx, s.DB, func(tx pgx.Tx) error {
		// Generate order number
		var counter int
		dateStr := time.Now().UTC().Format("2006-01-02")
		
		err := tx.QueryRow(ctx, `
			INSERT INTO daily_order_counters (date, counter)
			VALUES ($1, 1)
			ON CONFLICT (date) DO UPDATE 
			SET counter = daily_order_counters.counter + 1
			RETURNING counter
		`, dateStr).Scan(&counter)
		if err != nil {
			return fmt.Errorf("failed to generate order number: %w", err)
		}

		orderNumber = fmt.Sprintf(constants.OrderNumberFormat,
			time.Now().UTC().Format("20060102"), counter)

		// Insert order
		err = tx.QueryRow(ctx, `
			INSERT INTO orders (number, customer_name, type, table_number, delivery_address, total_amount, priority, status)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			RETURNING id
		`, orderNumber, req.CustomerName, req.OrderType, req.TableNumber, 
			req.DeliveryAddress, totalAmount, priority, constants.OrderStatusReceived).Scan(&orderID)
		if err != nil {
			return fmt.Errorf("failed to insert order: %w", err)
		}

		// Insert order items
		for _, item := range req.Items {
			_, err := tx.Exec(ctx, `
				INSERT INTO order_items (order_id, name, quantity, price)
				VALUES ($1, $2, $3, $4)
			`, orderID, item.Name, item.Quantity, item.Price)
			if err != nil {
				return fmt.Errorf("failed to insert order item: %w", err)
			}
		}

		// Log initial status
		_, err = tx.Exec(ctx, `
			INSERT INTO order_status_log (order_id, status, changed_by)
			VALUES ($1, $2, $3)
		`, orderID, constants.OrderStatusReceived, constants.ServiceOrderService)
		if err != nil {
			return fmt.Errorf("failed to log order status: %w", err)
		}

		return nil
	})

	if err != nil {
		logger.LogError("db_transaction_failed", "Failed to store order in database", err, requestID)
		return nil, errors.New("internal system error")
	}

	// Publish order message to RabbitMQ
	if err := s.publishOrderMessage(ctx, req, orderNumber, totalAmount, priority, requestID); err != nil {
		logger.LogError("rabbitmq_publish_failed", "Failed to publish order to RabbitMQ", err, requestID,
			"order_number", orderNumber)
		return nil, errors.New("internal system error")
	}

	logger.LogDebug("order_published", "Order published to RabbitMQ", requestID,
		"order_number", orderNumber,
		"priority", priority)

	return &models.OrderResponse{
		OrderNumber: orderNumber,
		Status:      constants.OrderStatusReceived,
		TotalAmount: totalAmount,
	}, nil
}

// publishOrderMessage publishes an order to RabbitMQ
func (s *Service) publishOrderMessage(ctx context.Context, req models.OrderRequest, orderNumber string, 
	totalAmount float64, priority int, requestID string) error {
	
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

	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	routingKey := fmt.Sprintf("%s.%s.%d", 
		constants.RoutingKeyKitchen, req.OrderType, priority)

	publishing := amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent,
		Priority:     uint8(priority),
		Body:         body,
		Timestamp:    time.Now().UTC(),
		MessageId:    requestID,
	}

	// Publish with retry
	publishCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	err = s.Amqp.PublishWithContext(publishCtx, 
		constants.ExchangeOrdersTopic, routingKey, false, false, publishing)
	
	if err != nil {
		return fmt.Errorf("failed to publish: %w", err)
	}

	return nil
}

// validateOrder validates an order request
func (s *Service) validateOrder(req models.OrderRequest) error {
	// Validate customer name
	if len(req.CustomerName) < constants.CustomerNameMinLength || 
		len(req.CustomerName) > constants.CustomerNameMaxLength {
		return fmt.Errorf("customer_name must be between %d and %d characters",
			constants.CustomerNameMinLength, constants.CustomerNameMaxLength)
	}

	if !customerNameRegex.MatchString(req.CustomerName) {
		return errors.New("customer_name contains invalid characters (only letters, numbers, spaces, hyphens, and apostrophes allowed)")
	}

	// Validate order type
	if req.OrderType != constants.OrderTypeDineIn &&
		req.OrderType != constants.OrderTypeTakeout &&
		req.OrderType != constants.OrderTypeDelivery {
		return fmt.Errorf("order_type must be one of: %s, %s, %s",
			constants.OrderTypeDineIn, constants.OrderTypeTakeout, constants.OrderTypeDelivery)
	}

	// Validate items count
	if len(req.Items) < constants.OrderItemsMin || len(req.Items) > constants.OrderItemsMax {
		return fmt.Errorf("items count must be between %d and %d",
			constants.OrderItemsMin, constants.OrderItemsMax)
	}

	// Validate each item
	for i, item := range req.Items {
		if len(item.Name) < constants.ItemNameMinLength || len(item.Name) > constants.ItemNameMaxLength {
			return fmt.Errorf("item[%d].name must be between %d and %d characters",
				i, constants.ItemNameMinLength, constants.ItemNameMaxLength)
		}
		if item.Quantity < constants.ItemQuantityMin || item.Quantity > constants.ItemQuantityMax {
			return fmt.Errorf("item[%d].quantity must be between %d and %d",
				i, constants.ItemQuantityMin, constants.ItemQuantityMax)
		}
		if item.Price < constants.ItemPriceMin || item.Price > constants.ItemPriceMax {
			return fmt.Errorf("item[%d].price must be between %.2f and %.2f",
				i, constants.ItemPriceMin, constants.ItemPriceMax)
		}
	}

	// Validate conditional fields based on order type
	switch req.OrderType {
	case constants.OrderTypeDineIn:
		if req.TableNumber == nil || *req.TableNumber < constants.TableNumberMin || 
			*req.TableNumber > constants.TableNumberMax {
			return fmt.Errorf("table_number is required for dine_in and must be between %d and %d",
				constants.TableNumberMin, constants.TableNumberMax)
		}
		if req.DeliveryAddress != "" {
			return errors.New("delivery_address must not be present for dine_in orders")
		}

	case constants.OrderTypeDelivery:
		if len(req.DeliveryAddress) < constants.DeliveryAddressMinLength {
			return fmt.Errorf("delivery_address is required for delivery orders and must be at least %d characters",
				constants.DeliveryAddressMinLength)
		}
		if req.TableNumber != nil {
			return errors.New("table_number must not be present for delivery orders")
		}

	case constants.OrderTypeTakeout:
		if req.TableNumber != nil {
			return errors.New("table_number must not be present for takeout orders")
		}
		if req.DeliveryAddress != "" {
			return errors.New("delivery_address must not be present for takeout orders")
		}
	}

	return nil
}

// calculateTotalAmount calculates the total amount for an order
func calculateTotalAmount(items []models.OrderItemRequest) float64 {
	total := 0.0
	for _, item := range items {
		total += item.Price * float64(item.Quantity)
	}
	return total
}

// calculatePriority determines order priority based on total amount
func calculatePriority(totalAmount float64) int {
	if totalAmount > constants.PriorityHighThreshold {
		return constants.PriorityHigh
	} else if totalAmount > constants.PriorityMediumThreshold {
		return constants.PriorityMedium
	}
	return constants.PriorityLow
}

// GenerateRequestID creates a unique request ID for tracing
func GenerateRequestID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp-based ID
		return fmt.Sprintf("req-%d", time.Now().UnixNano())
	}
	return "req-" + hex.EncodeToString(b)
}
