package rabbitmq

import (
	"fmt"
	"log/slog"

	amqp "github.com/rabbitmq/amqp091-go"
)

type Client struct {
	Conn *amqp.Connection
}

func Connect(url string) (*Client, error) {
	conn, err := amqp.Dial(url)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to rabbitmq: %w", err)
	}

	slog.Info("Connected to RabbitMQ", 
		slog.String("action", "rabbitmq_connected"),
	)

	return &Client{Conn: conn}, nil
}

func (c *Client) Close() error {
	return c.Conn.Close()
}

func (c *Client) CreateChannel() (*amqp.Channel, error) {
	ch, err := c.Conn.Channel()
	if err != nil {
		return nil, fmt.Errorf("failed to open channel: %w", err)
	}
	return ch, nil
}