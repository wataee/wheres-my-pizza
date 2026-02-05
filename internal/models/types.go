package models

import "time"

// OrderItemRequest represents an item in an order request
type OrderItemRequest struct {
	Name     string  `json:"name"`
	Quantity int     `json:"quantity"`
	Price    float64 `json:"price"`
}

// OrderRequest represents a customer order request
type OrderRequest struct {
	CustomerName    string             `json:"customer_name"`
	OrderType       string             `json:"order_type"`
	Items           []OrderItemRequest `json:"items"`
	TableNumber     *int               `json:"table_number,omitempty"`
	DeliveryAddress string             `json:"delivery_address,omitempty"`
}

// OrderResponse represents the response after creating an order
type OrderResponse struct {
	OrderNumber string  `json:"order_number"`
	Status      string  `json:"status"`
	TotalAmount float64 `json:"total_amount"`
}

// OrderMessage represents an order message published to RabbitMQ
type OrderMessage struct {
	OrderNumber     string             `json:"order_number"`
	CustomerName    string             `json:"customer_name"`
	OrderType       string             `json:"order_type"`
	TableNumber     *int               `json:"table_number"`
	DeliveryAddress string             `json:"delivery_address"`
	Items           []OrderItemRequest `json:"items"`
	TotalAmount     float64            `json:"total_amount"`
	Priority        int                `json:"priority"`
}

// OrderStatusUpdate represents a status change notification
type OrderStatusUpdate struct {
	OrderNumber         string    `json:"order_number"`
	OldStatus           string    `json:"old_status"`
	NewStatus           string    `json:"new_status"`
	ChangedBy           string    `json:"changed_by"`
	Timestamp           time.Time `json:"timestamp"`
	EstimatedCompletion time.Time `json:"estimated_completion,omitempty"`
}

// OrderHistoryEntry represents a single status change in order history
type OrderHistoryEntry struct {
	Status    string    `json:"status"`
	Timestamp time.Time `json:"timestamp"`
	ChangedBy string    `json:"changed_by"`
}

// OrderStatusResponse represents the current status of an order
type OrderStatusResponse struct {
	OrderNumber         string    `json:"order_number"`
	CurrentStatus       string    `json:"current_status"`
	UpdatedAt           time.Time `json:"updated_at"`
	EstimatedCompletion time.Time `json:"estimated_completion,omitempty"`
	ProcessedBy         string    `json:"processed_by,omitempty"`
}

// WorkerStatus represents the status of a kitchen worker
type WorkerStatus struct {
	WorkerName      string    `json:"worker_name"`
	Status          string    `json:"status"`
	OrdersProcessed int       `json:"orders_processed"`
	LastSeen        time.Time `json:"last_seen"`
}

// HealthCheckResponse represents the health status of a service
type HealthCheckResponse struct {
	Status    string            `json:"status"`
	Timestamp time.Time         `json:"timestamp"`
	Service   string            `json:"service"`
	Checks    map[string]string `json:"checks"`
}
