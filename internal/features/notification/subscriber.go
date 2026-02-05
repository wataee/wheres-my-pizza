package notification

import (
	"context"
	"encoding/json"
	"fmt"
	"restaurant-system/internal/logger"
	"restaurant-system/internal/models"

	amqp "github.com/rabbitmq/amqp091-go"
)

type Subscriber struct {
	Amqp *amqp.Channel
}

func NewSubscriber(ch *amqp.Channel) *Subscriber {
	return &Subscriber{Amqp: ch}
}

func (s *Subscriber) Run(ctx context.Context) error {
	err := s.Amqp.ExchangeDeclare("notifications_fanout", "fanout", true, false, false, false, nil)
	if err != nil {
		return err
	}

	q, err := s.Amqp.QueueDeclare("notifications_queue", false, false, false, false, nil)
	if err != nil {
		return err
	}

	err = s.Amqp.QueueBind(q.Name, "", "notifications_fanout", false, nil)
	if err != nil {
		return err
	}

	msgs, err := s.Amqp.Consume(q.Name, "", false, false, false, false, nil)
	if err != nil {
		return err
	}

	logger.LogInfo("service_started", "Notification Subscriber started", "")

	for {
		select {
		case <-ctx.Done():
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
		logger.LogError("message_processing_failed", "Invalid JSON", err, "")
		d.Nack(false, false)
		return
	}

	logger.LogInfo("notification_received", "Received status update", "", "order_number", update.OrderNumber, "new_status", update.NewStatus)

	fmt.Printf("Notification for order %s: Status changed from '%s' to '%s' by %s.\n",
		update.OrderNumber, update.OldStatus, update.NewStatus, update.ChangedBy)

	d.Ack(false)
}