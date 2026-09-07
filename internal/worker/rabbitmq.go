package worker

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/artni96/GophProfile/internal/models"
	amqp "github.com/rabbitmq/amqp091-go"
	"go.uber.org/zap"
)

type BrokerI interface {
	Produce(ctx context.Context, m models.Message) error
}

type Broker struct {
	conn     *amqp.Connection
	ch       *amqp.Channel
	logger   *zap.Logger
	ex       string
	binding  string
	q        string
	dlq      string
	confirms chan amqp.Confirmation
}

func NewBroker(logger *zap.Logger) (*Broker, error) {
	broker := &Broker{
		logger: logger,
	}
	err := broker.Init()
	if err != nil {
		return nil, err
	}
	return broker, nil
}

func (b *Broker) Close() {
	if b.conn != nil {
		b.conn.Close()
	}
}

func (b *Broker) Init() error {
	var errs []string
	user, ok := os.LookupEnv("RABBITMQ_USER")
	if !ok {
		errs = append(errs, "RABBITMQ_USER is required")
	}
	password, ok := os.LookupEnv("RABBITMQ_PASSWORD")
	if !ok {
		errs = append(errs, "RABBITMQ_PASSWORD is required")
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "\n"))
	}
	conn, err := amqp.Dial(fmt.Sprintf("amqp://%s:%s@localhost:5672/", user, password))
	if err != nil {
		return err
	}
	ch, err := conn.Channel()
	if err != nil {
		b.logger.Error("failed to open channel", zap.Error(err))
		return fmt.Errorf("failed to open channel: %w", err)
	}

	if err = ch.Confirm(false); err != nil {
		b.logger.Error("failed to init confirmations", zap.Error(err))
		return fmt.Errorf("failed to init confirmations: %w", err)
	}
	confirms := ch.NotifyPublish(make(chan amqp.Confirmation, 100))
	b.confirms = confirms

	if err = ch.ExchangeDeclare("images.dlx", "direct", true, false, false, false, nil); err != nil {
		log.Fatal(err)
	}
	b.dlq = "images.dlq"
	if _, err = ch.QueueDeclare(b.dlq, true, false, false, false, nil); err != nil {
		log.Fatal(err)
	}

	if err = ch.QueueBind(b.dlq, "failed", "images.dlx", false, nil); err != nil {
		log.Fatal(err)
	}

	b.ex = "images.direct"
	if err = ch.ExchangeDeclare(b.ex, "direct", true, false, false, false, nil); err != nil {
		b.logger.Error("failed to declare exchange", zap.Error(err))
		return fmt.Errorf("failed to declare exchange: %w", err)
	}

	dlxArgs := amqp.Table{
		"x-dead-letter-exchange":    "images.dlx",
		"x-dead-letter-routing-key": "failed",
	}

	b.q = "images.main"
	q, err := ch.QueueDeclare(b.q, true, false, false, false, dlxArgs)
	if err != nil {
		b.logger.Error("failed to declare queue", zap.Error(err))
		return fmt.Errorf("failed to declare queue: %w", err)
	}
	b.binding = "main"
	if err = ch.QueueBind(q.Name, b.binding, b.ex, false, nil); err != nil {
		b.logger.Error("failed to bind queue", zap.Error(err))
		return fmt.Errorf("failed to bind queue: %w", err)
	}
	b.ch = ch
	b.conn = conn
	return nil
}

func (b *Broker) Produce(ctx context.Context, m models.Message) error {
	brokerCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	err := b.ch.PublishWithContext(brokerCtx, b.ex, "main", true, false, amqp.Publishing{
		DeliveryMode: amqp.Persistent,
		Body:         m.Body,
		ContentType:  "text/plain",
		Headers: amqp.Table{
			"avatar_id": m.AvatarID.String(),
			"user_id":   m.UserID,
			"action":    string(m.Action),
		},
		MessageId: m.ID.String(),
	})
	if err != nil {
		b.logger.Error("failed to publish message", zap.Error(err))
		return fmt.Errorf("failed to publish message: %w", err)
	}

	select {
	case c := <-b.confirms:
		if c.Ack {
			b.logger.Debug("message has been delivered to broker successfully")
		} else {
			b.logger.Error("failed to deliver message to broker")
		}
	case <-ctx.Done():
		log.Println("failed to deliver message to broker: timeout is out")
	}
	return nil
}
