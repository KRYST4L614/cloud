package s3

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"cloud/internal/domain"
	"cloud/internal/metrics"
)

var ErrNotFound = domain.ErrNotFound

type Store struct {
	client   *awss3.Client
	uploader *transfermanager.Client
	bucket   string
}

func New(endpoint, region, accessKey, secretKey, bucket string) *Store {
	client := awss3.New(awss3.Options{
		BaseEndpoint: aws.String(endpoint),
		Region:       region,
		Credentials:  credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
		UsePathStyle: true,

		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	})
	return &Store{
		client: client,
		uploader: transfermanager.New(client, func(u *transfermanager.Options) {
			u.PartSizeBytes = 16 << 20
			u.MultipartUploadThreshold = 16 << 20
			u.Concurrency = 3
			u.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
		}),
		bucket: bucket,
	}
}

func (s *Store) EnsureBucket(ctx context.Context) error {
	if _, err := s.client.HeadBucket(ctx, &awss3.HeadBucketInput{Bucket: &s.bucket}); err == nil {
		return nil
	}
	_, err := s.client.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: &s.bucket})
	var owned *types.BucketAlreadyOwnedByYou
	var exists *types.BucketAlreadyExists
	if err != nil && !errors.As(err, &owned) && !errors.As(err, &exists) {
		return fmt.Errorf("create bucket %q: %w", s.bucket, err)
	}
	return nil
}

func (s *Store) Bucket() string { return s.bucket }

func (s *Store) Ping(ctx context.Context) error {
	_, err := s.client.HeadBucket(ctx, &awss3.HeadBucketInput{Bucket: &s.bucket})
	return err
}

func (s *Store) Put(ctx context.Context, key, contentType string, r io.Reader) error {
	_, err := s.uploader.UploadObject(ctx, &transfermanager.UploadObjectInput{
		Bucket:      &s.bucket,
		Key:         &key,
		Body:        r,
		ContentType: aws.String(contentType),
	})
	observe("put", err)
	return err
}

func (s *Store) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := s.client.GetObject(ctx, &awss3.GetObjectInput{Bucket: &s.bucket, Key: &key})
	var noKey *types.NoSuchKey
	if errors.As(err, &noKey) {
		observe("get", nil)
		return nil, ErrNotFound
	}
	observe("get", err)
	if err != nil {
		return nil, err
	}
	return out.Body, nil
}

func (s *Store) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &awss3.DeleteObjectInput{Bucket: &s.bucket, Key: &key})
	observe("delete", err)
	return err
}

func observe(op string, err error) {
	result := "ok"
	if err != nil {
		result = "error"
	}
	metrics.StorageOps.WithLabelValues(op, result).Inc()
}
