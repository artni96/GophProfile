package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"

	"github.com/artni96/GophProfile/internal/config"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type S3Client struct {
	client     *s3.S3
	logger     *slog.Logger
	BucketName string
	tracer     trace.Tracer
}

func NewS3Client(cfg *config.Config, logger *slog.Logger, bucketName string, tracer trace.Tracer) (*S3Client, error) {
	s3cfg := &aws.Config{
		Region:           aws.String(cfg.S3.Region),
		Endpoint:         aws.String(cfg.S3.Endpoint),
		S3ForcePathStyle: aws.Bool(*cfg.S3.S3ForcePathStyle),
		Credentials:      credentials.NewStaticCredentials(cfg.S3.AccessKey, cfg.S3.SecretKey, ""),
		DisableSSL:       aws.Bool(!cfg.S3.DisableSSL),
	}

	sess := session.Must(session.NewSession(s3cfg))
	client := s3.New(sess)
	_, err := client.ListBuckets(&s3.ListBucketsInput{})
	if err != nil {
		fmt.Println(err.Error())
		return nil, fmt.Errorf("failed to check s3 connection: %w", err)
	}

	return &S3Client{
		client:     client,
		logger:     logger,
		BucketName: bucketName,
		tracer:     tracer,
	}, nil
}

func (s *S3Client) Save(ctx context.Context, objectKey string, reader io.ReadSeeker) error {
	ctx, span := s.tracer.Start(ctx, "S3Client.Save")
	defer span.End()

	span.AddEvent("s3.uploading")
	span.SetAttributes(attribute.String("s3.uploading.objectKey", objectKey))
	defer span.End()
	_, err := s.client.PutObjectWithContext(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.BucketName),
		Key:    aws.String(objectKey),
		Body:   reader,
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		s.logger.Debug("failed to upload object", "error", err)
		return fmt.Errorf("failed to upload object: %v", err)
	}
	span.SetStatus(codes.Ok, "")
	s.logger.Debug(fmt.Sprintf("Successfully uploaded to %s/%s", s.BucketName, objectKey))
	return nil
}

func (s *S3Client) Get(ctx context.Context, objectKey string) ([]byte, error) {
	ctx, span := s.tracer.Start(ctx, "S3Client.Get")
	defer span.End()
	span.SetAttributes(attribute.String("s3.get.objectKey", objectKey))
	result, err := s.client.GetObjectWithContext(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.BucketName),
		Key:    aws.String(objectKey),
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		s.logger.Debug("failed to download object", "error", err)
		return nil, fmt.Errorf("failed to download object: %v", err)
	}
	defer result.Body.Close()

	buf := &bytes.Buffer{}
	_, err = io.Copy(buf, result.Body)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		s.logger.Debug("failed to download object", "error", err)
		return nil, fmt.Errorf("failed to read object data: %v", err)
	}
	span.SetStatus(codes.Ok, "")
	s.logger.Debug(fmt.Sprintf("Successfully downloaded %s/%s (%d bytes)\n", s.BucketName, objectKey, buf.Len()))
	return buf.Bytes(), nil
}

func (s *S3Client) Check(ctx context.Context) (bool, error) {
	ctx, span := s.tracer.Start(ctx, "s3.health_check")
	defer span.End()
	_, err := s.client.ListBuckets(&s3.ListBucketsInput{})
	if err != nil {
		span.SetAttributes(attribute.String("s3.health_check_error", err.Error()))
		span.SetStatus(codes.Error, err.Error())
		s.logger.Debug("failed to list buckets", "error", err)
		return false, err
	}
	span.SetAttributes(attribute.Bool("s3.is_healthy", err == nil))
	span.SetStatus(codes.Unset, "")
	return true, nil
}

func (s *S3Client) GetAddr() string {
	return s.client.Endpoint
}

func (s *S3Client) GetBucketName() string {
	return s.BucketName
}

func (s *S3Client) Delete(ctx context.Context, objectKey string) error {
	ctx, span := s.tracer.Start(ctx, "S3Client.Delete")
	defer span.End()
	span.SetAttributes(attribute.String("s3.delete.objectKey", objectKey))

	_, err := s.client.DeleteObjectWithContext(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.BucketName),
		Key:    aws.String(objectKey),
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		s.logger.Debug("failed to delete object", "error", err)
		return fmt.Errorf("failed to delete object: %v", err)
	}
	span.SetStatus(codes.Ok, "")
	s.logger.Debug(fmt.Sprintf("Successfully deleted %s/%s", s.BucketName, objectKey))
	return nil
}
