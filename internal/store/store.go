package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

const maxContentBytes = 1 << 20 // 1MB

// Client wraps the S3 client and bucket name.
type Client struct {
	s3     *s3.Client
	bucket string
}

// New creates an S3 store client. If endpoint is non-empty it overrides the
// endpoint (for local dev with MinIO).
func New(ctx context.Context, bucket, endpoint string) (*Client, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}

	var s3Opts []func(*s3.Options)
	if endpoint != "" {
		s3Opts = append(s3Opts, func(o *s3.Options) {
			o.BaseEndpoint = &endpoint
			o.UsePathStyle = true // MinIO requires path-style addressing
		})
	}

	return &Client{
		s3:     s3.NewFromConfig(cfg, s3Opts...),
		bucket: bucket,
	}, nil
}

// PutContent uploads string content to the given S3 key.
func (c *Client) PutContent(ctx context.Context, key, content string) error {
	if len(content) > maxContentBytes {
		return ErrTooLarge
	}
	_, err := c.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        &c.bucket,
		Key:           &key,
		Body:          bytes.NewReader([]byte(content)),
		ContentLength: ptr(int64(len(content))),
		ContentType:   ptr("text/plain; charset=utf-8"),
	})
	if err != nil {
		return fmt.Errorf("put content %q: %w", key, err)
	}
	return nil
}

// GetContent downloads and returns the string content at the given S3 key.
func (c *Client) GetContent(ctx context.Context, key string) (string, error) {
	out, err := c.s3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &c.bucket,
		Key:    &key,
	})
	if err != nil {
		var nsk *s3types.NoSuchKey
		if errors.As(err, &nsk) {
			return "", ErrNotFound
		}
		// Note: S3 can also return a generic NotFound (e.g. bucket doesn't exist)
		// which won't match NoSuchKey. In practice this server owns the bucket, so
		// a missing bucket is a misconfiguration rather than a missing object.
		return "", fmt.Errorf("get content %q: %w", key, err)
	}
	defer func() { _ = out.Body.Close() }()

	// Guard against oversized objects that shouldn't exist but could if
	// someone wrote directly to the bucket bypassing the API.
	limited := io.LimitReader(out.Body, maxContentBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return "", fmt.Errorf("read content %q: %w", key, err)
	}
	if len(data) > maxContentBytes {
		return "", ErrTooLarge
	}
	return string(data), nil
}

// DeleteContent removes the object at the given S3 key. Deleting a
// non-existent key is not an error (S3 DeleteObject is idempotent).
func (c *Client) DeleteContent(ctx context.Context, key string) error {
	_, err := c.s3.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: &c.bucket,
		Key:    &key,
	})
	if err != nil {
		return fmt.Errorf("delete content %q: %w", key, err)
	}
	return nil
}

func ptr[T any](v T) *T { return &v }
