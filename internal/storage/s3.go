package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/artni96/GophProfile/internal/config"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"go.uber.org/zap"
)

type S3Client struct {
	client     *s3.S3
	logger     *zap.Logger
	BucketName string
}

func NewS3Client(cfg *config.Config, logger *zap.Logger, bucketName string) (*S3Client, error) {
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
		return nil, fmt.Errorf("failed to check s3 connection: %w", err)
	}

	return &S3Client{
		client:     client,
		logger:     logger,
		BucketName: bucketName,
	}, nil
}

func (s *S3Client) Save(ctx context.Context, objectKey string, reader io.ReadSeeker) error {
	_, err := s.client.PutObjectWithContext(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.BucketName),
		Key:    aws.String(objectKey),
		Body:   reader,
	})

	if err != nil {
		s.logger.Debug("failed to upload object", zap.Error(err))
		return fmt.Errorf("failed to upload object: %v", err)
	}

	s.logger.Debug(fmt.Sprintf("Successfully uploaded to %s/%s", s.BucketName, objectKey))
	return nil
}

func (s *S3Client) Get(ctx context.Context, objectKey string) ([]byte, error) {
	result, err := s.client.GetObjectWithContext(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.BucketName),
		Key:    aws.String(objectKey),
	})
	if err != nil {
		s.logger.Debug("failed to download object", zap.Error(err))
		return nil, fmt.Errorf("failed to download object: %v", err)
	}
	defer result.Body.Close()

	buf := &bytes.Buffer{}
	_, err = io.Copy(buf, result.Body)
	if err != nil {
		s.logger.Debug("failed to download object", zap.Error(err))
		return nil, fmt.Errorf("failed to read object data: %v", err)
	}

	s.logger.Debug(fmt.Sprintf("Successfully downloaded %s/%s (%d bytes)\n", s.BucketName, objectKey, buf.Len()))
	return buf.Bytes(), nil
}

func (s *S3Client) Check() (bool, error) {
	_, err := s.client.ListBuckets(&s3.ListBucketsInput{})
	if err != nil {
		s.logger.Debug("failed to list buckets", zap.Error(err))
		return false, err
	}
	return true, nil
}

func (s *S3Client) GetAddr() string {
	return s.client.Endpoint
}

func (s *S3Client) GetBucketName() string {
	return s.BucketName
}
