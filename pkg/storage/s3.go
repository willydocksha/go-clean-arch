// package storage provides an s3-compatible object storage client
// (aws s3, minio, localstack, cloudflare r2, etc.)
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// s3 connection options
type S3Config struct {
	Bucket          string
	Region          string
	Endpoint        string // custom endpoint for minio / localstack, empty for aws
	PresignEndpoint string // public endpoint used in presigned urls, defaults to endpoint
	AccessKeyID     string
	SecretAccessKey string
	ForcePathStyle  bool // required by minio
}

// metadata of a stored object
type ObjectInfo struct {
	Key          string    `json:"key"`
	Size         int64     `json:"size"`
	ContentType  string    `json:"content_type,omitempty"`
	LastModified time.Time `json:"last_modified"`
}

// s3 client wrapper
type S3 struct {
	client  *s3.Client
	presign *s3.PresignClient
	bucket  string
}

// create a new s3 client
func NewS3(ctx context.Context, cfg S3Config) (*S3, error) {
	// base aws config (falls back to default credential chain when keys are empty)
	opts := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(cfg.Region)}
	if cfg.AccessKeyID != "" {
		opts = append(opts, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		))
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}

	// client used for actual operations
	client := s3.NewFromConfig(awsCfg, clientOptions(cfg.Endpoint, cfg.ForcePathStyle))

	// presign client may use a different (public) endpoint
	presignEndpoint := cfg.PresignEndpoint
	if presignEndpoint == "" {
		presignEndpoint = cfg.Endpoint
	}
	presignClient := s3.NewPresignClient(s3.NewFromConfig(awsCfg, clientOptions(presignEndpoint, cfg.ForcePathStyle)))

	return &S3{client: client, presign: presignClient, bucket: cfg.Bucket}, nil
}

// build per-client options for custom endpoint and path style
func clientOptions(endpoint string, pathStyle bool) func(*s3.Options) {
	return func(o *s3.Options) {
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
		}
		o.UsePathStyle = pathStyle
	}
}

// check that the bucket is reachable (used by readiness probe)
func (s *S3) Ping(ctx context.Context) error {
	_, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(s.bucket)})
	return err
}

// create the bucket when it does not exist (handy for local minio)
func (s *S3) EnsureBucket(ctx context.Context) error {
	if err := s.Ping(ctx); err == nil {
		return nil
	}
	_, err := s.client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(s.bucket)})
	var owned *types.BucketAlreadyOwnedByYou
	if err != nil && !errors.As(err, &owned) {
		return fmt.Errorf("create bucket %q: %w", s.bucket, err)
	}
	return nil
}

// upload an object; body must be seekable so the sdk can sign it
func (s *S3) Upload(ctx context.Context, key string, body io.ReadSeeker, size int64, contentType string) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(key),
		Body:          body,
		ContentLength: aws.Int64(size),
		ContentType:   aws.String(contentType),
	})
	if err != nil {
		return fmt.Errorf("s3 put %q: %w", key, err)
	}
	return nil
}

// download an object, caller must close the reader
func (s *S3) Download(ctx context.Context, key string) (io.ReadCloser, ObjectInfo, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, ObjectInfo{}, mapError(key, err)
	}
	info := ObjectInfo{
		Key:          key,
		Size:         aws.ToInt64(out.ContentLength),
		ContentType:  aws.ToString(out.ContentType),
		LastModified: aws.ToTime(out.LastModified),
	}
	return out.Body, info, nil
}

// read a whole object into memory (fine for small files)
func (s *S3) ReadAll(ctx context.Context, key string) ([]byte, ObjectInfo, error) {
	body, info, err := s.Download(ctx, key)
	if err != nil {
		return nil, info, err
	}
	defer body.Close()
	data, err := io.ReadAll(body)
	return data, info, err
}

// read a whole object and return its bytes plus content type
func (s *S3) Get(ctx context.Context, key string) ([]byte, string, error) {
	data, info, err := s.ReadAll(ctx, key)
	return data, info.ContentType, err
}

// get object metadata without downloading it
func (s *S3) Stat(ctx context.Context, key string) (ObjectInfo, error) {
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return ObjectInfo{}, mapError(key, err)
	}
	return ObjectInfo{
		Key:          key,
		Size:         aws.ToInt64(out.ContentLength),
		ContentType:  aws.ToString(out.ContentType),
		LastModified: aws.ToTime(out.LastModified),
	}, nil
}

// report whether an object exists
func (s *S3) Exists(ctx context.Context, key string) (bool, error) {
	_, err := s.Stat(ctx, key)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// delete an object (no error if it does not exist)
func (s *S3) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("s3 delete %q: %w", key, err)
	}
	return nil
}

// list objects under a prefix (max 1000 per call)
func (s *S3) List(ctx context.Context, prefix string, limit int32) ([]ObjectInfo, error) {
	out, err := s.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket:  aws.String(s.bucket),
		Prefix:  aws.String(prefix),
		MaxKeys: aws.Int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("s3 list %q: %w", prefix, err)
	}

	// map sdk objects to our type
	items := make([]ObjectInfo, 0, len(out.Contents))
	for _, o := range out.Contents {
		items = append(items, ObjectInfo{
			Key:          aws.ToString(o.Key),
			Size:         aws.ToInt64(o.Size),
			LastModified: aws.ToTime(o.LastModified),
		})
	}
	return items, nil
}

// presigned get url; when filename is set the browser downloads it with that name
func (s *S3) PresignGet(ctx context.Context, key string, expiry time.Duration, filename string) (string, error) {
	in := &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)}
	if filename != "" {
		in.ResponseContentDisposition = aws.String(mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	}
	req, err := s.presign.PresignGetObject(ctx, in, s3.WithPresignExpires(expiry))
	if err != nil {
		return "", fmt.Errorf("s3 presign get %q: %w", key, err)
	}
	return req.URL, nil
}

// presigned put url so the frontend can upload directly to s3
func (s *S3) PresignPut(ctx context.Context, key, contentType string, expiry time.Duration) (string, error) {
	req, err := s.presign.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		ContentType: aws.String(contentType),
	}, s3.WithPresignExpires(expiry))
	if err != nil {
		return "", fmt.Errorf("s3 presign put %q: %w", key, err)
	}
	return req.URL, nil
}

// translate "not found" sdk errors into fs.ErrNotExist
func mapError(key string, err error) error {
	var nsk *types.NoSuchKey
	var nf *types.NotFound
	var apiErr smithy.APIError
	if errors.As(err, &nsk) || errors.As(err, &nf) ||
		(errors.As(err, &apiErr) && (apiErr.ErrorCode() == "NotFound" || apiErr.ErrorCode() == "NoSuchKey")) {
		return fmt.Errorf("s3 object %q: %w", key, fs.ErrNotExist)
	}
	return fmt.Errorf("s3 object %q: %w", key, err)
}
