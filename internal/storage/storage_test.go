package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/H8rsh100/arkive/internal/storage"
)

func TestFakeStorageClient(t *testing.T) {
	client := storage.NewFakeStorageClient()
	ctx := context.Background()

	bucket := "my-test-bucket"
	prefix := "postgres-daily-backup/"

	// Seed 3 backups with different timestamps
	t1 := time.Now().Add(-3 * time.Hour)
	t2 := time.Now().Add(-2 * time.Hour)
	t3 := time.Now().Add(-1 * time.Hour)

	client.SeedObject(bucket, prefix+"backup-1.sql.gz", t1)
	client.SeedObject(bucket, prefix+"backup-2.sql.gz", t2)
	client.SeedObject(bucket, prefix+"backup-3.sql.gz", t3)

	objs, err := client.ListObjects(ctx, bucket, prefix)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if len(objs) != 3 {
		t.Fatalf("expected 3 objects, got %d", len(objs))
	}

	// Verify chronological order (oldest first)
	if objs[0].Key != prefix+"backup-1.sql.gz" {
		t.Errorf("expected oldest object first, got %s", objs[0].Key)
	}

	// Delete oldest object
	err = client.DeleteObjects(ctx, bucket, []string{objs[0].Key})
	if err != nil {
		t.Fatalf("failed to delete object: %v", err)
	}

	remaining := client.GetCount(bucket, prefix)
	if remaining != 2 {
		t.Errorf("expected 2 remaining objects, got %d", remaining)
	}
}
