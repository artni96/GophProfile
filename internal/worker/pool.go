package worker

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"uuid"

	"github.com/artni96/GophProfile/internal/broker"
	"github.com/artni96/GophProfile/internal/models"
	"github.com/artni96/GophProfile/pkg/interfaces"
	amqp "github.com/rabbitmq/amqp091-go"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

type Pool struct {
	Broker       *broker.Broker
	WorkerNumber int
	DlqWorkerNum int
	Eg           *errgroup.Group
	service      interfaces.ServiceI
	logger       *zap.Logger
}

func NewPool(broker *broker.Broker, eg *errgroup.Group, service interfaces.ServiceI, logger *zap.Logger) *Pool {
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
				wp.logger.Error("worker failed", zap.Error(err))
			}
		})
	}
	for i := 0; i < wp.DlqWorkerNum; i++ {
		wg.Go(func() {
			err := wp.dlqWorker(ctx)
			if err != nil {
				wp.logger.Error("dlq worker failed", zap.Error(err))
			}
		})
	}
	return &wg
}

func (wp *Pool) dlqWorker(ctx context.Context) error {
	wp.logger.Debug("worker for dlq started")
	dlqMsgs, err := wp.Broker.Ch.Consume(
		wp.Broker.Dlq,
		"",
		false,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		wp.logger.Debug(fmt.Sprintf("failed to consume messages from %s", wp.Broker.Dlq), zap.Error(err))
		return fmt.Errorf("failed to consume messages from %s: %w", wp.Broker.Dlq, err)
	}
	for {
		select {
		//case <-gfCtx.Done():
		//	wp.broker.logger.Debug("dql worker process done: grace context done")
		//	return nil
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
				wp.logger.Debug("failed to prepare message", zap.Error(err))
				continue
			}
			var isSuccess bool
			switch msg.Action {
			case models.Upload:
				for i := 0; i < 3; i++ {
					err = wp.service.UploadThumbnailsToS3(ctx, msg)
					if err != nil {
						wp.logger.Debug("failed to upload thumbnails", zap.Error(err))
						continue
					}
					isSuccess = true
					break
				}
			case models.Remove:
				for i := 0; i < 3; i++ {
					err = wp.service.DeleteFromS3(ctx, msg.S3Key)
					if err != nil {
						wp.logger.Debug("worker process delete failed", zap.Error(err))
						continue
					}
					isSuccess = true
					break
				}
			}
			if !isSuccess {
				switch msg.Action {
				case models.Upload:
					wp.logger.Debug("failed to upload thumbnail", zap.String("s3key", msg.S3Key), zap.Error(err))
				case models.Remove:
					wp.logger.Debug("failed to delete object", zap.Error(err))
				}

				if err = d.Nack(false, false); err != nil {
					wp.logger.Debug("worker process nack failed", zap.Error(err))
				} else {
					wp.logger.Debug(
						fmt.Sprintf("message goes to %s", wp.Broker.Dlq),
						zap.String("message id", msg.ID.String()))
				}
				continue
			}

			err = d.Ack(false)
			if err != nil {
				wp.logger.Error("worker process ack failed", zap.Error(err))
				err = d.Nack(false, false)
				if err != nil {
					wp.logger.Error("failed to delete message", zap.Error(err))
				}
				continue
			}
			wp.logger.Debug(fmt.Sprintf("msg from %s handled", wp.Broker.Dlq))
		}
	}
}

func (wp *Pool) worker(ctx context.Context, workerID int) error {
	wp.logger.Debug("worker started", zap.Int("worker number", workerID))
	ch, err := wp.Broker.Conn.Channel()
	if err != nil {
		wp.logger.Debug("failed to init worker channel", zap.Error(err))
		return fmt.Errorf("failed to create channel for worker with id%d: %w", workerID, err)
	}
	if err = ch.Qos(1, 0, false); err != nil {
		wp.logger.Debug("failed to set QoS", zap.Error(err))
		return fmt.Errorf("failed to set QoS for worker with id%d: %w", workerID, err)
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
		wp.logger.Debug("failed to consume messages", zap.Error(err))
		return fmt.Errorf("failed to consume message: %w", err)
	}

	for {
		select {
		//case <-gfCtx.Done():
		//	wp.broker.logger.Debug("worker process done: grace context done", zap.Int("worker id", workerID))
		//	return nil
		case <-ctx.Done():
			wp.logger.Debug("worker process done: main context done", zap.Int("worker id", workerID))
			return nil
		case d, ok := <-msgs:
			if !ok {
				wp.logger.Debug("broker queue is closed", zap.Int("worker id", workerID))
				return fmt.Errorf("broker queue is closed")
			}

			msg, err := wp.prepareMsg(d)
			if err != nil {
				wp.logger.Debug("failed to prepare message", zap.Error(err))
				continue
			}

			//var isUploaded bool
			//for i := 0; i < 3; i++ {
			//	err = wp.service.UploadThumbnailsToS3(ctx, msg)
			//	if err != nil {
			//		wp.logger.Debug("failed to upload thumbnails", zap.Error(err))
			//		continue
			//	}
			//	isUploaded = true
			//	break
			//}
			var isSuccess bool
			switch msg.Action {
			case models.Upload:
				for i := 0; i < 3; i++ {
					err = wp.service.UploadThumbnailsToS3(ctx, msg)
					if err != nil {
						wp.logger.Debug("failed to upload thumbnails", zap.Error(err))
						continue
					}
					isSuccess = true
					break
				}
			case models.Remove:
				for i := 0; i < 3; i++ {
					err = wp.service.DeleteFromS3(ctx, msg.S3Key)
					if err != nil {
						wp.logger.Debug("worker process delete failed", zap.Error(err))
						continue
					}
					isSuccess = true
					break
				}
			}
			if !isSuccess {
				wp.logger.Debug("failed to upload thumbnails", zap.Error(err))
				if err = d.Nack(false, false); err != nil {
					wp.logger.Debug("worker process nack failed", zap.Error(err))
				} else {
					wp.logger.Debug(
						fmt.Sprintf("message goes to %s", wp.Broker.Dlq),
						zap.String("message id", msg.ID.String()),
						zap.Int("worker id", workerID))
				}
				continue
			}

			err = d.Ack(false)
			if err != nil {
				wp.logger.Error("worker process ack failed", zap.Error(err))
				if err = d.Nack(false, false); err != nil {
					wp.logger.Debug("worker process nack failed", zap.Error(err))
				} else {
					wp.logger.Debug(
						fmt.Sprintf("message goes to %s", wp.Broker.Dlq),
						zap.String("message id", msg.ID.String()),
						zap.Int("worker id", workerID))
				}
				continue
			}
			wp.logger.Debug(
				"worker process ack done",
				zap.Int("worker id", workerID),
				zap.String("message id", msg.ID.String()),
			)
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
