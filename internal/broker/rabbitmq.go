package broker

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/artni96/GophProfile/internal/models"
	amqp "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type BrokerI interface {
	Produce(ctx context.Context, m models.Message) error
}

type Broker struct {
	Conn     *amqp.Connection
	Ch       *amqp.Channel
	logger   *slog.Logger
	ex       string
	binding  string
	MainQ    string
	Dlq      string
	confirms chan amqp.Confirmation
	tracer   trace.Tracer
}

func NewBroker(logger *slog.Logger, tracer trace.Tracer) (*Broker, error) {
	broker := &Broker{
		logger: logger,
		tracer: tracer,
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
		b.logger.Error("failed to open channel", "error", err)
		return fmt.Errorf("failed to open channel: %w", err)
	}

	if err = ch.Confirm(false); err != nil {
		b.logger.Error("failed to init confirmations", "error", err)
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
		b.logger.Error("failed to declare exchange", "error", err)
		return fmt.Errorf("failed to declare exchange: %w", err)
	}

	dlxArgs := amqp.Table{
		"x-dead-letter-exchange":    "images.dlx",
		"x-dead-letter-routing-key": "failed",
	}

	b.MainQ = "images.main"
	q, err := ch.QueueDeclare(b.MainQ, true, false, false, false, dlxArgs)
	if err != nil {
		b.logger.Error("failed to declare queue", "error", err)
		return fmt.Errorf("failed to declare queue: %w", err)
	}
	b.binding = "main"
	if err = ch.QueueBind(q.Name, b.binding, b.ex, false, nil); err != nil {
		b.logger.Error("failed to bind queue", "error", err)
		return fmt.Errorf("failed to bind queue: %w", err)
	}
	b.Ch = ch
	b.Conn = conn
	return nil
}

func (b *Broker) Produce(ctx context.Context, m models.Message) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	brokerCtx, span := b.tracer.Start(ctx, "Message producing", trace.WithSpanKind(trace.SpanKindProducer))
	defer span.End()
	span.AddEvent("sending message to broker")
	span.SetAttributes(
		attribute.String("id", m.ID.String()),
		attribute.String("avatar_id", m.AvatarID.String()),
		attribute.String("user_id", m.UserID),
		attribute.String("action", string(m.Action)),
		attribute.String("s3key", m.S3Key),
	)
	headers := amqp.Table{
		"avatar_id": m.AvatarID.String(),
		"user_id":   m.UserID,
		"action":    string(m.Action),
		"s3_key":    m.S3Key,
	}
	otel.GetTextMapPropagator().Inject(brokerCtx, Propagator(headers))

	err := b.Ch.PublishWithContext(brokerCtx, b.ex, "main", true, false, amqp.Publishing{
		DeliveryMode: amqp.Persistent,
		ContentType:  "application/octet-stream",
		Body:         m.Body,
		Headers:      headers,
		MessageId:    m.ID.String(),
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		b.logger.Error("failed to publish message", "error", err)
		return fmt.Errorf("failed to publish message: %w", err)
	}

	select {
	case c, ok := <-b.confirms:
		if !ok {
			span.RecordError(fmt.Errorf("confirm channel closed; broker connection lost"))
			span.SetStatus(codes.Error, "confirm channel closed; broker connection lost")
			return errors.New("confirm channel closed; broker connection lost")
		}
		if !c.Ack {
			span.RecordError(fmt.Errorf("failed to ack message"))
			span.SetStatus(codes.Error, "failed to ack message")
			return errors.New("failed to ack message")
		}
		span.AddEvent("message confirmed")
		b.logger.Debug("message delivered to broker")
		return nil
	case <-brokerCtx.Done():
		span.RecordError(brokerCtx.Err())
		span.SetStatus(codes.Error, "publish confirm timeout")
		return fmt.Errorf("publish confirm timeout: %w", brokerCtx.Err())
	}
}

func (b *Broker) Check(ctx context.Context) error {
	ctx, span := b.tracer.Start(ctx, "broker.health_check")
	defer span.End()

	ch, err := b.Conn.Channel()
	if err != nil {
		span.SetAttributes(attribute.String("broker.health_check_error", err.Error()))
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	defer ch.Close()
	span.SetAttributes(attribute.Bool("broker.is_healty", err == nil))
	span.SetStatus(codes.Ok, "")
	return nil
}
