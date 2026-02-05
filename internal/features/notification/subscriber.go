package notification

import (
	"context"
	"encoding/json"
	"fmt"
	"restaurant-system/internal/constants"
	"restaurant-system/internal/logger"
	"restaurant-system/internal/models"
	"restaurant-system/internal/rabbitmq"

	amqp "github.com/rabbitmq/amqp091-go"
)

type Subscriber struct {
	Amqp *amqp.Channel
}

func NewSubscriber(ch *amqp.Channel) *Subscriber {
	return &Subscriber{Amqp: ch}
}

func (s *Subscriber) Run(ctx context.Context) error {
	if err := rabbitmq.DeclareExchange(s.Amqp, constants.ExchangeNotificationsFanout, "fanout"); err != nil {
		logger.LogError("startup", "Failed to declare notifications exchange", err, "startup")
		return err
	}

	q, err := rabbitmq.DeclareQueue(s.Amqp, constants.QueueNotifications, nil)
	if err != nil {
		logger.LogError("startup", "Failed to declare notifications queue", err, "startup")
		return err
	}

	if err := rabbitmq.BindQueue(s.Amqp, q.Name, "", constants.ExchangeNotificationsFanout); err != nil {
		logger.LogError("startup", "Failed to bind notifications queue", err, "startup")
		return err
	}

	msgs, err := s.Amqp.Consume(q.Name, "", false, false, false, false, nil)
	if err != nil {
		logger.LogError("startup", "Failed to start consuming", err, "startup")
		return err
	}

	logger.LogInfo("service_started", "Notification Subscriber started", "startup")

	for {
		select {
		case <-ctx.Done():
			logger.LogInfo("graceful_shutdown", "Notification Subscriber stopping", "shutdown")
			return nil
		case d, ok := <-msgs:
			if !ok {
				return nil
			}
			s.processMessage(d)
		}
	}
}

func (s *Subscriber) processMessage(d amqp.Delivery) {
	var update models.OrderStatusUpdate
	if err := json.Unmarshal(d.Body, &update); err != nil {
		logger.LogError("message_processing_failed", "Invalid JSON in notification", err, "")
		d.Nack(false, false)
		return
	}

	logger.LogDebug("notification_received", "Received status update", "",
		"order_number", update.OrderNumber,
		"old_status", update.OldStatus,
		"new_status", update.NewStatus,
		"changed_by", update.ChangedBy)

	fmt.Printf("Notification for order %s: Status changed from '%s' to '%s' by %s.\n",
		update.OrderNumber, update.OldStatus, update.NewStatus, update.ChangedBy)

	d.Ack(false)
}
