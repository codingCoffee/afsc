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
  "strconv"
  "strings"
  "time"

  "github.com/aws/aws-sdk-go-v2/aws"
  "github.com/aws/aws-sdk-go-v2/config"
  "github.com/aws/aws-sdk-go-v2/service/s3"
  "github.com/aws/aws-sdk-go-v2/service/s3/types"
  "github.com/aws/smithy-go"
)

type Storage interface {
  Put(ctx context.Context, key string, body io.ReadSeeker, size int64, contentType string) error
}

// Reader streams stored originals. rng is a single "bytes=a-b" range, or "" for the whole object.
type Reader interface {
  Get(ctx context.Context, key, rng string) (*Object, error)
  Head(ctx context.Context, key string) (*Object, error)
}

// ErrNotFound means the key doesn't exist. ErrRange means rng can't be satisfied.
var (
  ErrNotFound = errors.New("storage: object not found")
  ErrRange    = errors.New("storage: range not satisfiable")
)

// Object is a stored file. Body is nil for Head. ContentRange is set when a range was served.
type Object struct {
  Body         io.ReadCloser
  Size         int64 // total object size
  Length       int64 // bytes in Body
  ContentRange string
  LastModified time.Time
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

// Check verifies the bucket is reachable with the current credentials. It lists one key rather than using
// HeadBucket, because a HEAD error has no body and every failure reads as a bare 403.
func (s *S3) Check(ctx context.Context) error {
  in := &s3.ListObjectsV2Input{Bucket: aws.String(s.bucket), MaxKeys: aws.Int32(1)}
  if _, err := s.client.ListObjectsV2(ctx, in); err != nil {
    return fmt.Errorf("bucket %q is not reachable: %w", s.bucket, err)
  }
  return nil
}

func (s *S3) Get(ctx context.Context, key, rng string) (*Object, error) {
  in := &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)}
  if rng != "" {
    in.Range = aws.String(rng)
  }
  out, err := s.client.GetObject(ctx, in)
  if err != nil {
    return nil, s3Err("get", key, err)
  }
  o := &Object{Body: out.Body, Length: aws.ToInt64(out.ContentLength), ContentRange: aws.ToString(out.ContentRange),
    LastModified: aws.ToTime(out.LastModified)}
  o.Size = o.Length
  if o.ContentRange != "" {
    if _, total, ok := strings.Cut(o.ContentRange, "/"); ok {
      o.Size, _ = strconv.ParseInt(total, 10, 64)
    }
  }
  return o, nil
}

func (s *S3) Head(ctx context.Context, key string) (*Object, error) {
  out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
  if err != nil {
    return nil, s3Err("head", key, err)
  }
  n := aws.ToInt64(out.ContentLength)
  return &Object{Size: n, Length: n, LastModified: aws.ToTime(out.LastModified)}, nil
}

func s3Err(op, key string, err error) error {
  var nsk *types.NoSuchKey
  var nf *types.NotFound
  var api smithy.APIError
  switch {
  case errors.As(err, &nsk), errors.As(err, &nf):
    return ErrNotFound
  case errors.As(err, &api) && api.ErrorCode() == "InvalidRange":
    return ErrRange
  }
  return fmt.Errorf("%s %s: %w", op, key, err)
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
