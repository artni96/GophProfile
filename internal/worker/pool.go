package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"time"
	"uuid"

	"github.com/artni96/GophProfile/internal/broker"
	"github.com/artni96/GophProfile/internal/models"
	"github.com/artni96/GophProfile/pkg/interfaces"
	amqp "github.com/rabbitmq/amqp091-go"
	"golang.org/x/sync/errgroup"
)

type Pool struct {
	Broker       *broker.Broker
	WorkerNumber int
	DlqWorkerNum int
	Eg           *errgroup.Group
	service      interfaces.ServiceI
	logger       *slog.Logger
}

func NewPool(broker *broker.Broker, eg *errgroup.Group, service interfaces.ServiceI, logger *slog.Logger) *Pool {
	return &Pool{
		Broker:       broker,
		WorkerNumber: runtime.NumCPU() - 1,
		DlqWorkerNum: 1,
		Eg:           eg,
		service:      service,
		logger:       logger,
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
			msg, err := wp.prepareMsg(d)
			if err != nil {
				if err = d.Ack(false); err != nil {
					wp.logger.Error("dlq worker: failed to ack", "error", err)
					return fmt.Errorf("dlq worker: ack after prepare failure: %w", err)
				}
				wp.logger.Debug("dlq worker failed to prepare message", "error", err)
				continue
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
				wp.logger.Debug("attempt failed", "attempt", attempt, "error", err)
				time.Sleep(time.Duration((attempt+1)*(attempt+1)) * time.Second)
			}
			if !isSuccess {
				switch msg.Action {
				case models.Upload:
					wp.logger.Debug("failed to upload thumbnail", "s3key", msg.S3Key, "error", err)
				case models.Remove:
					wp.logger.Debug("failed to delete object", "error", err)
				}

				if err = d.Nack(false, false); err != nil {
					wp.logger.Debug("worker process nack failed", "error", err)
				} else {
					wp.logger.Debug(
						fmt.Sprintf("message goes to %s", wp.Broker.Dlq), "message id", msg.ID.String())
				}
				continue
			}

			err = d.Ack(false)
			if err != nil {
				wp.logger.Error("worker process ack failed", "error", err)
				err = d.Nack(false, false)
				if err != nil {
					wp.logger.Error("failed to delete message", "error", err)
				}
				continue
			}
			wp.logger.Debug(fmt.Sprintf("msg from %s handled", wp.Broker.Dlq))
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

			msg, err := wp.prepareMsg(d)
			if err != nil {
				wp.logger.Debug(
					"failed to prepare message for consumer", "worker id", workerID, "error", err)
				_ = d.Nack(false, false)
				continue
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
				wp.logger.Debug("attempt failed", "attempt", attempt, "error", err)
				time.Sleep(time.Duration((attempt+1)*(attempt+1)) * time.Second)
			}

			if !isSuccess {
				_ = d.Nack(false, false) // DLQ
				continue
			}

			if err = d.Ack(false); err != nil {
				wp.logger.Error("ack failed", "error", err)
				return fmt.Errorf("worker %d: ack: %w", workerID, err)
			}
		}
	}
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
