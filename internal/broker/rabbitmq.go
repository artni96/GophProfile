package broker

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
	Conn     *amqp.Connection
	Ch       *amqp.Channel
	logger   *zap.Logger
	ex       string
	binding  string
	MainQ    string
	Dlq      string
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
	if b.Conn != nil {
		b.Conn.Close()
	}
}

func (b *Broker) Init() error {
	var errs []string
	user, ok := os.LookupEnv("RABBITMQ_USER")
	if !ok {
		b.logger.Debug("RABBITMQ_USER environment variable is not set")
		errs = append(errs, "RABBITMQ_USER is required")
	}
	password, ok := os.LookupEnv("RABBITMQ_PASSWORD")
	if !ok {
		b.logger.Debug("RABBITMQ_PASSWORD environment variable is not set")
		errs = append(errs, "RABBITMQ_PASSWORD is required")
	}
	host, ok := os.LookupEnv("RABBITMQ_HOST")
	if !ok {
		b.logger.Debug("RABBITMQ_HOST environment variable is not set")
		errs = append(errs, "RABBITMQ_HOST is required")
	}
	port, ok := os.LookupEnv("RABBITMQ_INTERNAL_PORT")
	if !ok {
		b.logger.Debug("RABBITMQ_PORT environment variable is not set")
		errs = append(errs, "RABBITMQ_PORT environment variable is required")
	}
	if len(errs) > 0 {

		b.logger.Debug("failed to initialize RabbitMQ broker: no required environment variables found")
		return errors.New(strings.Join(errs, "\n"))
	}
	conn, err := amqp.Dial(fmt.Sprintf("amqp://%s:%s@%s:%s", user, password, host, port))
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
	b.Dlq = "images.dlq"
	if _, err = ch.QueueDeclare(b.Dlq, true, false, false, false, nil); err != nil {
		log.Fatal(err)
	}

	if err = ch.QueueBind(b.Dlq, "failed", "images.dlx", false, nil); err != nil {
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

	b.MainQ = "images.main"
	q, err := ch.QueueDeclare(b.MainQ, true, false, false, false, dlxArgs)
	if err != nil {
		b.logger.Error("failed to declare queue", zap.Error(err))
		return fmt.Errorf("failed to declare queue: %w", err)
	}
	b.binding = "main"
	if err = ch.QueueBind(q.Name, b.binding, b.ex, false, nil); err != nil {
		b.logger.Error("failed to bind queue", zap.Error(err))
		return fmt.Errorf("failed to bind queue: %w", err)
	}
	b.Ch = ch
	b.Conn = conn
	return nil
}

func (b *Broker) Produce(ctx context.Context, m models.Message) error {
	brokerCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	err := b.Ch.PublishWithContext(brokerCtx, b.ex, "main", true, false, amqp.Publishing{
		DeliveryMode: amqp.Persistent,
		Body:         m.Body,
		ContentType:  "text/plain",
		Headers: amqp.Table{
			"avatar_id": m.AvatarID.String(),
			"user_id":   m.UserID,
			"action":    string(m.Action),
			"s3_key":    m.S3Key,
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

func (b *Broker) Check() error {
	ch, err := b.Conn.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()
	return nil
}
