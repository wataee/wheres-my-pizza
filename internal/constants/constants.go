package constants

import "time"

// Order constants
const (
	// Order types
	OrderTypeDineIn   = "dine_in"
	OrderTypeTakeout  = "takeout"
	OrderTypeDelivery = "delivery"

	// Order statuses
	OrderStatusReceived  = "received"
	OrderStatusCooking   = "cooking"
	OrderStatusReady     = "ready"
	OrderStatusCompleted = "completed"
	OrderStatusCancelled = "cancelled"

	// Priorities
	PriorityLow    = 1
	PriorityMedium = 5
	PriorityHigh   = 10

	// Priority thresholds
	PriorityHighThreshold   = 100.0
	PriorityMediumThreshold = 50.0
)

// Validation constants
const (
	// Customer name validation
	CustomerNameMinLength = 1
	CustomerNameMaxLength = 100

	// Item validation
	ItemNameMinLength = 1
	ItemNameMaxLength = 50
	ItemQuantityMin   = 1
	ItemQuantityMax   = 10
	ItemPriceMin      = 0.01
	ItemPriceMax      = 999.99

	// Order items limits
	OrderItemsMin = 1
	OrderItemsMax = 20

	// Table number validation
	TableNumberMin = 1
	TableNumberMax = 100

	// Delivery address validation
	DeliveryAddressMinLength = 10
)

// Cooking time constants
const (
	CookingTimeDineIn   = 8 * time.Second
	CookingTimeTakeout  = 10 * time.Second
	CookingTimeDelivery = 12 * time.Second
)

// RabbitMQ constants
const (
	// Exchanges
	ExchangeOrdersTopic        = "orders_topic"
	ExchangeNotificationsFanout = "notifications_fanout"
	ExchangeDLX                = "dlx_exchange"

	// Queues
	QueueDeadLetter    = "dead_letter_queue"
	QueueNotifications = "notifications_queue"

	// Routing keys
	RoutingKeyDeadLetter = "dead_letter"
	RoutingKeyKitchen    = "kitchen"

	// Prefetch defaults
	DefaultPrefetchCount = 1
)

// Service names
const (
	ServiceOrderService          = "order-service"
	ServiceKitchenWorker         = "kitchen-worker"
	ServiceTrackingService       = "tracking-service"
	ServiceNotificationSubscriber = "notification-subscriber"
)

// HTTP constants
const (
	DefaultOrderServicePort    = 3000
	DefaultTrackingServicePort = 3002
)

// Worker constants
const (
	DefaultHeartbeatInterval = 30 * time.Second
	WorkerStatusOnline       = "online"
	WorkerStatusOffline      = "offline"
)

// Context timeout constants
const (
	ContextTimeoutDefault     = 30 * time.Second
	ContextTimeoutDB          = 10 * time.Second
	ContextTimeoutHTTP        = 15 * time.Second
	ContextTimeoutShutdown    = 5 * time.Second
	ContextTimeoutHealthCheck = 5 * time.Second
)

// Retry constants
const (
	MaxRetryAttempts       = 3
	RetryInitialBackoff    = 100 * time.Millisecond
	RetryMaxBackoff        = 5 * time.Second
	RetryBackoffMultiplier = 2
)

// Order number format
const (
	OrderNumberPrefix = "ORD"
	OrderNumberFormat = "ORD_%s_%03d" // ORD_YYYYMMDD_NNN
)
