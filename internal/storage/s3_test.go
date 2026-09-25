package storage

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/stretchr/testify/assert"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func newS3ClientWithEndpoint(endpoint string) *s3.S3 {
	sess := session.Must(session.NewSession(&aws.Config{
		Endpoint:         aws.String(endpoint),
		Region:           aws.String("us-east-1"),
		S3ForcePathStyle: aws.Bool(true),
		Credentials:      credentials.NewStaticCredentials("k", "s", ""),
	}))
	return s3.New(sess)
}

func newTestClient(t *testing.T, s3Client *s3.S3) (*S3Client, *tracetest.SpanRecorder) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	return &S3Client{
		client:     s3Client,
		BucketName: "test-bucket",
		tracer:     tp.Tracer("test"),
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, recorder
}

func TestSave(t *testing.T) {
	var path, method string
	var body []byte

	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		path = r.URL.Path
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	s3client, _ := newTestClient(t, newS3ClientWithEndpoint(testServer.URL))

	err := s3client.Save(context.Background(), "/test/path", bytes.NewReader([]byte("test")))
	assert.NoError(t, err)
	assert.Equal(t, http.MethodPut, method)
	assert.Equal(t, "/test-bucket/test/path", path)
	assert.Equal(t, "test", string(body))
}

func TestGet(t *testing.T) {
	var method, path string

	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		path = r.URL.Path
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("hello world"))
	}))

	s3client, _ := newTestClient(t, newS3ClientWithEndpoint(testServer.URL))

	res, err := s3client.Get(context.Background(), "/test/path")

	assert.NoError(t, err)
	assert.Equal(t, "hello world", string(res))
	assert.Equal(t, http.MethodGet, method)
	assert.Equal(t, "/test-bucket/test/path", path)
}

func TestDelete(t *testing.T) {
	var method, path string

	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		path = r.URL.Path
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("hello world"))
	}))

	s3client, _ := newTestClient(t, newS3ClientWithEndpoint(testServer.URL))
	err := s3client.Delete(context.Background(), "/test/path")
	assert.NoError(t, err)
	assert.Equal(t, http.MethodDelete, method)
	assert.Equal(t, "/test-bucket/test/path", path)
}

func TestGetAddr(t *testing.T) {
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("hello world"))
	}))
	s3client, _ := newTestClient(t, newS3ClientWithEndpoint(testServer.URL))
	assert.Equal(t, s3client.GetAddr(), s3client.client.Endpoint)
}

func TestGetBucketName(t *testing.T) {
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("hello world"))
	}))
	s3client, _ := newTestClient(t, newS3ClientWithEndpoint(testServer.URL))
	assert.Equal(t, s3client.GetBucketName(), "test-bucket")
}
