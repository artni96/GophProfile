package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"uuid"

	"github.com/artni96/GophProfile/internal/broker"
	"github.com/artni96/GophProfile/internal/models"
	"github.com/artni96/GophProfile/pkg/interfaces"
	amqp "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"
)

type Pool struct {
	Broker       *broker.Broker
	WorkerNumber int
	DlqWorkerNum int
	Eg           *errgroup.Group
	service      interfaces.ServiceI
	logger       *slog.Logger
	tracer       trace.Tracer
}

func NewPool(
	broker *broker.Broker,
	eg *errgroup.Group,
	service interfaces.ServiceI,
	logger *slog.Logger,
	tracer trace.Tracer,
) *Pool {
	return &Pool{
		Broker:       broker,
		WorkerNumber: runtime.NumCPU() - 1,
		DlqWorkerNum: 1,
		Eg:           eg,
		service:      service,
		logger:       logger,
		tracer:       tracer,
	}
}

func (wp *Pool) Launch(ctx context.Context) *sync.WaitGroup {
	var wg sync.WaitGroup

	for i := 0; i < wp.WorkerNumber; i++ {
		wg.Go(func() {
			err := wp.worker(ctx, i)
			if err != nil {
				wp.logger.Error("worker failed", "error", err)
			}
		})
	}
	for i := 0; i < wp.DlqWorkerNum; i++ {
		wg.Go(func() {
			err := wp.dlqWorker(ctx)
			if err != nil {
				wp.logger.Error("dlq worker failed", "error", err)
			}
		})
	}
	return &wg
}

func (wp *Pool) dlqWorker(ctx context.Context) error {
	wp.logger.Debug("worker for dlq started")

	ch, err := wp.Broker.Conn.Channel()
	if err != nil {
		return fmt.Errorf("create channel for dlq worker: %w", err)
	}
	defer ch.Close()
	if err = ch.Qos(1, 0, false); err != nil {
		return fmt.Errorf("set QoS for dlq worker: %w", err)
	}

	dlqMsgs, err := ch.Consume(
		wp.Broker.Dlq,
		"",
		false,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		wp.logger.Debug(fmt.Sprintf("failed to consume messages from %s", wp.Broker.Dlq), "error", err)
		return fmt.Errorf("failed to consume messages from %s: %w", wp.Broker.Dlq, err)
	}
	for {
		select {
		case <-ctx.Done():
			wp.logger.Debug("dlq worker process done: main context done")
			return nil
		case d, ok := <-dlqMsgs:
			if !ok {
				wp.logger.Debug(fmt.Sprintf("%s is closed", wp.Broker.Dlq))
				return fmt.Errorf("broker queue is closed")
			}
			headers := broker.Propagator(d.Headers)
			remoteCtx := otel.GetTextMapPropagator().Extract(ctx, headers)
			spanCtx, span := wp.tracer.Start(remoteCtx, "dql-worker")
			err = wp.handleMessage(spanCtx, d, "dlq")
			if err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
			} else {
				span.SetStatus(codes.Ok, "")
			}
			span.End()
		}
	}
}

func (wp *Pool) worker(ctx context.Context, workerID int) error {
	wp.logger.Debug("worker started", "worker number", workerID)

	ch, err := wp.Broker.Conn.Channel()
	if err != nil {
		return fmt.Errorf("create channel for worker %d: %w", workerID, err)
	}
	defer ch.Close()

	if err = ch.Qos(1, 0, false); err != nil {
		return fmt.Errorf("set QoS for worker %d: %w", workerID, err)
	}

	msgs, err := ch.Consume(
		wp.Broker.MainQ,
		"",
		false,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		return fmt.Errorf("consume for worker %d: %w", workerID, err)
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case d, ok := <-msgs:
			if !ok {
				return fmt.Errorf("worker %d: delivery channel closed", workerID)
			}
			headers := broker.Propagator(d.Headers)
			remoteCtx := otel.GetTextMapPropagator().Extract(ctx, headers)
			spanCtx, span := wp.tracer.Start(remoteCtx, "worker-"+strconv.Itoa(workerID))
			err = wp.handleMessage(spanCtx, d, strconv.Itoa(workerID))
			if err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
			} else {
				span.SetStatus(codes.Ok, "")
			}
			span.End()
		}
	}
}

func (wp *Pool) handleMessage(
	ctx context.Context,
	d amqp.Delivery,
	workerID string,
) error {
	msg, err := wp.prepareMsg(d)
	if err != nil {
		wp.logger.Debug("failed to prepare message for consumer", "worker id", workerID, "error", err)
		switch workerID {
		case "dlq":
			if ackErr := d.Ack(false); ackErr != nil {
				wp.logger.Error("dlq worker: failed to ack message", "ack error", ackErr, "error", err)
				return fmt.Errorf("dlq worker: failed to ack message: %w", ackErr)
			}
			wp.logger.Debug("dlq worker: failed to prepare message - message will be lost", "error", err)
		default:
			if nackErr := d.Nack(false, false); nackErr != nil {
				wp.logger.Error(
					"failed to nack message", "worker_id", workerID, "nack error", nackErr, "error", err)
				return fmt.Errorf("failed to nack message -  worker_id %s: %w", workerID, err)
			}
			wp.logger.Debug("message goes to dlq", "worker id", workerID)
		}
		return fmt.Errorf("failed to prepare message for consumer, worker_id - %s: %w", workerID, err)
	}

	isSuccess := false
	for attempt := 0; attempt < 3; attempt++ {
		if ctx.Err() != nil {
			break
		}
		switch msg.Action {
		case models.Upload:
			err = wp.service.UploadThumbnailsToS3(ctx, msg)
		case models.Remove:
			err = wp.service.DeleteFromS3(ctx, msg.AvatarID)
		default:
			err = fmt.Errorf("unknown action: %v", msg.Action)
		}
		if err == nil {
			isSuccess = true
			break
		}
		attemptIn := time.Duration((attempt+1)*(attempt+1)) * time.Second
		wp.logger.Debug(
			"attempt failed", "attempt", attempt, "next attempt in", attemptIn, "worker_id", workerID, "error", err)
		select {
		case <-time.After(attemptIn):
		case <-ctx.Done():
			wp.logger.Debug("context is done", "worker_id", workerID)
		}
	}

	if !isSuccess {
		switch msg.Action {
		case models.Upload:
			wp.logger.Debug("failed to upload thumbnail", "s3key", msg.S3Key, "worker_id", workerID)
		case models.Remove:
			wp.logger.Debug("failed to delete object", "worker_id", workerID)
		}
		if nackErr := d.Nack(false, false); nackErr != nil {
			switch workerID {
			case "dlq":
				wp.logger.Debug(
					"failed to nack message - message will be lost", "worker_id", workerID, "nack error", nackErr)
			default:
				wp.logger.Debug(
					"failed to nack message - message goes to dlq", "worker id", workerID, "nack error", nackErr)
			}
		}
		return fmt.Errorf("failed to handle message, worker_id - %s", workerID)
	}

	ackRrr := d.Ack(false)
	if ackRrr != nil {
		if nackErr := d.Nack(false, false); nackErr != nil {
			switch workerID {
			case "dlq":
				wp.logger.Debug("failed to nack message - message will be lost", "worker_id", workerID, "nack error", nackErr, "ack error", ackRrr)
			default:
				wp.logger.Debug("failed to nack message - message goes to dlq", "worker id", workerID, "nack error", nackErr, "ack error", ackRrr)
			}
			return fmt.Errorf("failed to handle message, worker_id - %s", workerID)
		}
		return fmt.Errorf("failed to ack message, worker_id - %s", workerID)
	}
	return nil
}

func (wp *Pool) prepareMsg(d amqp.Delivery) (msg models.Message, err error) {
	var errs []string
	strMsgID := d.MessageId
	msgID := uuid.MustParse(strMsgID)
	strAvatarID := d.Headers["avatar_id"].(string)
	avatarID := uuid.MustParse(strAvatarID)
	action := d.Headers["action"].(string)
	userID := d.Headers["user_id"].(string)
	s3Key := d.Headers["s3_key"].(string)
	if action != "" {
		msg.Action = models.ActionType(action)
	} else {
		errs = append(errs, fmt.Sprintf("failed to parse action from amqp.delivery, messageID: %s", d.MessageId))
	}

	if msgID != uuid.Nil() {
		msg.ID = msgID
	} else {
		errs = append(errs, fmt.Sprintf("failed to parse message id from amqp.delivery, messageID: %s", d.MessageId))
	}

	if avatarID != uuid.Nil() {
		msg.AvatarID = avatarID
	} else {
		errs = append(errs, fmt.Sprintf("failed to parse avatar id from amqp.delivery, messageID: %s", d.MessageId))
	}

	if action != "" {
		msg.Action = models.ActionType(action)
	} else {
		errs = append(errs, fmt.Sprintf("failed to parse action from amqp.delivery, messageID: %s", d.MessageId))
	}
	if models.ActionType(action) == models.Remove {
		if s3Key != "" {
			msg.S3Key = s3Key
		} else {
			errs = append(errs, fmt.Sprintf("failed to parse s3_key from amqp.delivery, messageID: %s", d.MessageId))
		}
	}

	msg.Body = d.Body
	msg.UserID = userID
	if len(errs) > 0 {
		err = errors.New(strings.Join(errs, "; "))
	}
	return msg, nil
}
