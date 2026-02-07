package rabbitmq

import (
	"context"
	"fmt"
	"restaurant-system/internal/logger"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	// Reconnection settings
	ReconnectDelay    = 5 * time.Second
	MaxReconnectDelay = 60 * time.Second
	ReconnectAttempts = 0 // 0 means infinite
	
	// Startup settings
	MaxStartupRetries = 15 // Пытаться подключиться 15 раз при старте
)

// Client represents a RabbitMQ client with auto-reconnection
type Client struct {
	url         string
	conn        *amqp.Connection
	channels    map[string]*amqp.Channel
	mu          sync.RWMutex
	reconnectCh chan struct{}
	closeCh     chan struct{}
	connected   bool
	ctx         context.Context
	cancel      context.CancelFunc
}

// Connect establishes a connection to RabbitMQ
func Connect(url string) (*Client, error) {
	ctx, cancel := context.WithCancel(context.Background())
	
	client := &Client{
		url:         url,
		channels:    make(map[string]*amqp.Channel),
		reconnectCh: make(chan struct{}, 1),
		closeCh:     make(chan struct{}),
		ctx:         ctx,
		cancel:      cancel,
	}

	// Retry logic for initial connection
	connected := false
	backoff := 2 * time.Second

	for i := 1; i <= MaxStartupRetries; i++ {
		if err := client.connect(); err != nil {
			logger.LogInfo("rabbitmq_startup_retry", 
				fmt.Sprintf("Failed to connect to RabbitMQ (attempt %d/%d), retrying in %v...", i, MaxStartupRetries, backoff), 
				"startup")
			
			time.Sleep(backoff)
			
			// Exponential backoff capped at 10s
			backoff *= 2
			if backoff > 10*time.Second {
				backoff = 10 * time.Second
			}
			continue
		}
		
		connected = true
		break
	}

	if !connected {
		cancel()
		return nil, fmt.Errorf("failed to connect to rabbitmq after %d attempts", MaxStartupRetries)
	}

	// Start connection monitor
	go client.monitorConnection()

	logger.LogInfo("rabbitmq_connected", "Connected to RabbitMQ", "startup")

	return client, nil
}

// connect performs the actual connection
func (c *Client) connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	conn, err := amqp.Dial(c.url)
	if err != nil {
		return fmt.Errorf("failed to connect to rabbitmq: %w", err)
	}

	c.conn = conn
	c.connected = true

	// Monitor connection closure
	go func() {
		closeErr := <-conn.NotifyClose(make(chan *amqp.Error))
		if closeErr != nil {
			logger.LogError("rabbitmq_connection_closed", "RabbitMQ connection closed", 
				fmt.Errorf(closeErr.Error()), "")
			
			c.mu.Lock()
			c.connected = false
			c.mu.Unlock()

			// Trigger reconnection
			select {
			case c.reconnectCh <- struct{}{}:
			default:
			}
		}
	}()

	return nil
}

// monitorConnection handles automatic reconnection
func (c *Client) monitorConnection() {
	reconnectDelay := ReconnectDelay
	attempts := 0

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-c.closeCh:
			return
		case <-c.reconnectCh:
			attempts++
			
			c.mu.RLock()
			connected := c.connected
			c.mu.RUnlock()

			if connected {
				attempts = 0
				reconnectDelay = ReconnectDelay
				continue
			}

			logger.LogInfo("rabbitmq_reconnecting", 
				fmt.Sprintf("Attempting to reconnect to RabbitMQ (attempt %d)", attempts), 
				"reconnect")

			if err := c.connect(); err != nil {
				logger.LogError("rabbitmq_reconnect_failed", 
					fmt.Sprintf("Failed to reconnect (attempt %d)", attempts), err, "reconnect")

				// Exponential backoff
				time.Sleep(reconnectDelay)
				reconnectDelay *= 2
				if reconnectDelay > MaxReconnectDelay {
					reconnectDelay = MaxReconnectDelay
				}

				// Trigger another reconnection attempt
				if ReconnectAttempts == 0 || attempts < ReconnectAttempts {
					select {
					case c.reconnectCh <- struct{}{}:
					default:
					}
				}
			} else {
				logger.LogInfo("rabbitmq_reconnected", "Successfully reconnected to RabbitMQ", "reconnect")
				attempts = 0
				reconnectDelay = ReconnectDelay
			}
		}
	}
}

// CreateChannel creates a new AMQP channel
func (c *Client) CreateChannel() (*amqp.Channel, error) {
	c.mu.RLock()
	conn := c.conn
	connected := c.connected
	c.mu.RUnlock()

	if !connected {
		return nil, fmt.Errorf("not connected to RabbitMQ")
	}

	ch, err := conn.Channel()
	if err != nil {
		return nil, fmt.Errorf("failed to open channel: %w", err)
	}

	return ch, nil
}

// CreateChannelWithRetry creates a channel with retry logic
func (c *Client) CreateChannelWithRetry(maxAttempts int) (*amqp.Channel, error) {
	backoff := 1 * time.Second

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		ch, err := c.CreateChannel()
		if err == nil {
			return ch, nil
		}

		if attempt < maxAttempts {
			logger.LogError("channel_creation_retry", 
				fmt.Sprintf("Failed to create channel (attempt %d/%d)", attempt, maxAttempts),
				err, "")
			time.Sleep(backoff)
			backoff *= 2
		}
	}

	return nil, fmt.Errorf("failed to create channel after %d attempts", maxAttempts)
}

// IsConnected returns the current connection status
func (c *Client) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.connected
}

// Close closes the RabbitMQ connection
func (c *Client) Close() error {
	c.cancel()
	close(c.closeCh)

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil && !c.conn.IsClosed() {
		logger.LogInfo("rabbitmq_closing", "Closing RabbitMQ connection", "shutdown")
		return c.conn.Close()
	}

	return nil
}

// DeclareExchange declares an exchange with standard settings
func DeclareExchange(ch *amqp.Channel, name, kind string) error {
	return ch.ExchangeDeclare(
		name,
		kind,
		true,  // durable
		false, // auto-deleted
		false, // internal
		false, // no-wait
		nil,   // arguments
	)
}

// DeclareQueue declares a queue with standard settings
func DeclareQueue(ch *amqp.Channel, name string, args amqp.Table) (amqp.Queue, error) {
	return ch.QueueDeclare(
		name,
		true,  // durable
		false, // delete when unused
		false, // exclusive
		false, // no-wait
		args,  // arguments
	)
}

// DeclareQueueWithDLX declares a queue with dead letter exchange configured
func DeclareQueueWithDLX(ch *amqp.Channel, queueName, dlxName, dlxRoutingKey string) (amqp.Queue, error) {
	args := amqp.Table{
		"x-dead-letter-exchange":    dlxName,
		"x-dead-letter-routing-key": dlxRoutingKey,
	}
	return DeclareQueue(ch, queueName, args)
}

// BindQueue binds a queue to an exchange
func BindQueue(ch *amqp.Channel, queueName, routingKey, exchangeName string) error {
	return ch.QueueBind(
		queueName,
		routingKey,
		exchangeName,
		false, // no-wait
		nil,   // arguments
	)
}

// PublishWithRetry publishes a message with retry logic
func PublishWithRetry(ctx context.Context, ch *amqp.Channel, exchange, routingKey string, msg amqp.Publishing, maxAttempts int) error {
	backoff := 100 * time.Millisecond

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err := ch.PublishWithContext(ctx, exchange, routingKey, false, false, msg)
		if err == nil {
			return nil
		}

		if attempt < maxAttempts {
			logger.LogError("publish_retry", 
				fmt.Sprintf("Failed to publish message (attempt %d/%d)", attempt, maxAttempts),
				err, "")
			time.Sleep(backoff)
			backoff *= 2
		}
	}

	return fmt.Errorf("failed to publish message after %d attempts", maxAttempts)
}