// Package storage keeps original media files in S3-compatible object storage.
package storage

import (
  "context"
  "crypto/rand"
  "encoding/hex"
  "errors"
  "fmt"
  "io"
  "os"

  "github.com/aws/aws-sdk-go-v2/aws"
  "github.com/aws/aws-sdk-go-v2/config"
  "github.com/aws/aws-sdk-go-v2/service/s3"
)

type Storage interface {
  Put(ctx context.Context, key string, body io.ReadSeeker, size int64, contentType string) error
}

// NewKey returns a random storage key; keys are never derived from user filenames (§7.4).
func NewKey() (string, error) {
  b := make([]byte, 16)
  if _, err := rand.Read(b); err != nil {
    return "", err
  }
  h := hex.EncodeToString(b)
  return "o/" + h[:2] + "/" + h, nil
}

type S3Config struct {
  Bucket    string
  Region    string
  Endpoint  string
  PathStyle bool
}

// S3ConfigFromEnv reads AFSC_S3_*. Credentials come from the default AWS chain, never from AFSC config.
func S3ConfigFromEnv() (S3Config, error) {
  c := S3Config{
    Bucket:    os.Getenv("AFSC_S3_BUCKET"),
    Region:    os.Getenv("AFSC_S3_REGION"),
    Endpoint:  os.Getenv("AFSC_S3_ENDPOINT"),
    PathStyle: os.Getenv("AFSC_S3_PATH_STYLE") == "true",
  }
  if c.Bucket == "" || c.Region == "" {
    return c, errors.New("AFSC_S3_BUCKET and AFSC_S3_REGION must be set")
  }
  return c, nil
}

type S3 struct {
  client *s3.Client
  bucket string
}

func NewS3(ctx context.Context, c S3Config) (*S3, error) {
  cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(c.Region))
  if err != nil {
    return nil, fmt.Errorf("load aws config: %w", err)
  }
  client := s3.NewFromConfig(cfg, func(o *s3.Options) {
    if c.Endpoint != "" {
      o.BaseEndpoint = aws.String(c.Endpoint)
    }
    o.UsePathStyle = c.PathStyle
    // S3-compatible servers don't all support the newer default request checksums
    o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
  })
  return &S3{client: client, bucket: c.Bucket}, nil
}

// Check verifies the bucket is reachable with the current credentials.
func (s *S3) Check(ctx context.Context) error {
  if _, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(s.bucket)}); err != nil {
    return fmt.Errorf("bucket %q is not reachable: %w", s.bucket, err)
  }
  return nil
}

func (s *S3) Put(ctx context.Context, key string, body io.ReadSeeker, size int64, contentType string) error {
  _, err := s.client.PutObject(ctx, &s3.PutObjectInput{
    Bucket:        aws.String(s.bucket),
    Key:           aws.String(key),
    Body:          body,
    ContentLength: aws.Int64(size),
    ContentType:   aws.String(contentType),
  })
  if err != nil {
    return fmt.Errorf("put %s: %w", key, err)
  }
  return nil
}
