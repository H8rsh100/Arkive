package storage

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// FakeStorageClient implements ObjectStorageClient in-memory for testing and local validation.
type FakeStorageClient struct {
	mu      sync.Mutex
	Objects map[string]map[string]ObjectInfo // bucket -> key -> ObjectInfo
}

// NewFakeStorageClient constructs an empty FakeStorageClient.
func NewFakeStorageClient() *FakeStorageClient {
	return &FakeStorageClient{
		Objects: make(map[string]map[string]ObjectInfo),
	}
}

func (f *FakeStorageClient) ListObjects(ctx context.Context, bucket, prefix string) ([]ObjectInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	bucketObjs, exists := f.Objects[bucket]
	if !exists {
		return []ObjectInfo{}, nil
	}

	var matched []ObjectInfo
	for key, info := range bucketObjs {
		if strings.HasPrefix(key, prefix) {
			matched = append(matched, info)
		}
	}

	sort.Slice(matched, func(i, j int) bool {
		return matched[i].LastModified.Before(matched[j].LastModified)
	})

	return matched, nil
}

func (f *FakeStorageClient) DeleteObjects(ctx context.Context, bucket string, keys []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	bucketObjs, exists := f.Objects[bucket]
	if !exists {
		return nil
	}

	for _, key := range keys {
		delete(bucketObjs, key)
	}

	return nil
}

func (f *FakeStorageClient) UploadObject(ctx context.Context, bucket, key string, data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if _, exists := f.Objects[bucket]; !exists {
		f.Objects[bucket] = make(map[string]ObjectInfo)
	}

	f.Objects[bucket][key] = ObjectInfo{
		Key:          key,
		LastModified: time.Now(),
		Size:         int64(len(data)),
	}

	return nil
}

// SeedObject adds an object to the fake storage with a specific timestamp for retention testing.
func (f *FakeStorageClient) SeedObject(bucket, key string, lastModified time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if _, exists := f.Objects[bucket]; !exists {
		f.Objects[bucket] = make(map[string]ObjectInfo)
	}

	f.Objects[bucket][key] = ObjectInfo{
		Key:          key,
		LastModified: lastModified,
		Size:         1024,
	}
}

// GetCount returns the total number of objects in a bucket prefix.
func (f *FakeStorageClient) GetCount(bucket, prefix string) int {
	objs, err := f.ListObjects(context.Background(), bucket, prefix)
	if err != nil {
		return 0
	}
	return len(objs)
}
