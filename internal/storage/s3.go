package storage

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// ObjectInfo represents metadata for a stored backup object.
type ObjectInfo struct {
	Key          string
	LastModified time.Time
	Size         int64
}

// ObjectStorageClient provides an abstraction for S3 object operations.
type ObjectStorageClient interface {
	ListObjects(ctx context.Context, bucket, prefix string) ([]ObjectInfo, error)
	DeleteObjects(ctx context.Context, bucket string, keys []string) error
	UploadObject(ctx context.Context, bucket, key string, data []byte) error
}

// S3Client implements ObjectStorageClient using AWS SDK v2.
type S3Client struct {
	api S3API
}

// S3API abstracts raw S3 service calls for testability.
type S3API interface {
	ListObjectsV2(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
	DeleteObjects(ctx context.Context, params *s3.DeleteObjectsInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error)
	PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
}

// NewS3Client creates a new S3Client wrapping an S3 service API instance.
func NewS3Client(api S3API) *S3Client {
	return &S3Client{api: api}
}

// ListObjects returns all objects under the given prefix sorted chronologically (oldest first).
func (c *S3Client) ListObjects(ctx context.Context, bucket, prefix string) ([]ObjectInfo, error) {
	if c.api == nil {
		return nil, fmt.Errorf("s3 API client is not initialized")
	}

	input := &s3.ListObjectsV2Input{
		Bucket: aws.String(bucket),
		Prefix: aws.String(prefix),
	}

	output, err := c.api.ListObjectsV2(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to list S3 objects in bucket %s: %w", bucket, err)
	}

	var results []ObjectInfo
	for _, obj := range output.Contents {
		key := aws.ToString(obj.Key)
		lastMod := time.Time{}
		if obj.LastModified != nil {
			lastMod = *obj.LastModified
		}
		results = append(results, ObjectInfo{
			Key:          key,
			LastModified: lastMod,
			Size:         aws.ToInt64(obj.Size),
		})
	}

	// Sort oldest first
	sort.Slice(results, func(i, j int) bool {
		return results[i].LastModified.Before(results[j].LastModified)
	})

	return results, nil
}

// DeleteObjects deletes multiple object keys from the specified bucket.
func (c *S3Client) DeleteObjects(ctx context.Context, bucket string, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	if c.api == nil {
		return fmt.Errorf("s3 API client is not initialized")
	}

	var objectIdentifiers []types.ObjectIdentifier
	for _, k := range keys {
		objectIdentifiers = append(objectIdentifiers, types.ObjectIdentifier{
			Key: aws.String(k),
		})
	}

	input := &s3.DeleteObjectsInput{
		Bucket: aws.String(bucket),
		Delete: &types.Delete{
			Objects: objectIdentifiers,
			Quiet:   aws.Bool(true),
		},
	}

	_, err := c.api.DeleteObjects(ctx, input)
	if err != nil {
		return fmt.Errorf("failed to delete S3 objects from bucket %s: %w", bucket, err)
	}

	return nil
}

// UploadObject uploads raw data to the specified key.
func (c *S3Client) UploadObject(ctx context.Context, bucket, key string, data []byte) error {
	if c.api == nil {
		return fmt.Errorf("s3 API client is not initialized")
	}

	input := &s3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	}

	_, err := c.api.PutObject(ctx, input)
	if err != nil {
		return fmt.Errorf("failed to upload object to %s/%s: %w", bucket, key, err)
	}

	return nil
}
