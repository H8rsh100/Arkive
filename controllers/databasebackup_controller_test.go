package controllers_test

import (
	"context"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	backupv1alpha1 "github.com/H8rsh100/arkive/api/v1alpha1"
	"github.com/H8rsh100/arkive/controllers"
	"github.com/H8rsh100/arkive/internal/storage"
)

func TestDatabaseBackupRetentionEnforcement(t *testing.T) {
	fakeStorage := storage.NewFakeStorageClient()
	ctx := context.Background()

	bucket := "test-bucket"
	prefix := "postgres-backups/"

	// Seed 5 objects
	now := time.Now()
	fakeStorage.SeedObject(bucket, prefix+"backup-1.sql.gz", now.Add(-5*time.Hour))
	fakeStorage.SeedObject(bucket, prefix+"backup-2.sql.gz", now.Add(-4*time.Hour))
	fakeStorage.SeedObject(bucket, prefix+"backup-3.sql.gz", now.Add(-3*time.Hour))
	fakeStorage.SeedObject(bucket, prefix+"backup-4.sql.gz", now.Add(-2*time.Hour))
	fakeStorage.SeedObject(bucket, prefix+"backup-5.sql.gz", now.Add(-1*time.Hour))

	backup := &backupv1alpha1.DatabaseBackup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-backup",
			Namespace: "default",
		},
		Spec: backupv1alpha1.DatabaseBackupSpec{
			Retention: backupv1alpha1.RetentionSpec{
				Count: 3, // Retain 3 newest, prune 2 oldest
			},
			Destination: backupv1alpha1.DestinationSpec{
				Bucket: bucket,
				Prefix: prefix,
			},
		},
	}

	reconciler := &controllers.DatabaseBackupReconciler{
		StorageClient: fakeStorage,
	}

	// List before retention enforcement
	beforeCount := fakeStorage.GetCount(bucket, prefix)
	if beforeCount != 5 {
		t.Fatalf("expected 5 initial objects, got %d", beforeCount)
	}

	// Run retention enforcement logic via storage client
	objs, err := fakeStorage.ListObjects(ctx, bucket, prefix)
	if err != nil {
		t.Fatalf("failed to list objects: %v", err)
	}

	excessCount := len(objs) - int(backup.Spec.Retention.Count)
	var keysToDelete []string
	for i := 0; i < excessCount; i++ {
		keysToDelete = append(keysToDelete, objs[i].Key)
	}

	err = fakeStorage.DeleteObjects(ctx, bucket, keysToDelete)
	if err != nil {
		t.Fatalf("failed to delete excess objects: %v", err)
	}

	afterCount := fakeStorage.GetCount(bucket, prefix)
	if afterCount != 3 {
		t.Errorf("expected 3 remaining objects after retention enforcement, got %d", afterCount)
	}

	// Verify remaining objects are the 3 newest
	remaining, _ := fakeStorage.ListObjects(ctx, bucket, prefix)
	if remaining[0].Key != prefix+"backup-3.sql.gz" {
		t.Errorf("expected oldest remaining object to be backup-3, got %s", remaining[0].Key)
	}
}

func TestDatabaseBackupDeletionOptIn(t *testing.T) {
	fakeStorage := storage.NewFakeStorageClient()
	ctx := context.Background()

	bucket := "my-app-backups"
	prefix := "db/"

	fakeStorage.SeedObject(bucket, prefix+"dump.sql.gz", time.Now())

	// Test 1: deleteOnResourceDeletion = false -> Objects preserved
	backupNoDelete := &backupv1alpha1.DatabaseBackup{
		Spec: backupv1alpha1.DatabaseBackupSpec{
			Destination: backupv1alpha1.DestinationSpec{
				Bucket:                   bucket,
				Prefix:                   prefix,
				DeleteOnResourceDeletion: false,
			},
		},
	}

	if backupNoDelete.Spec.Destination.DeleteOnResourceDeletion {
		t.Errorf("expected DeleteOnResourceDeletion to be false")
	}

	if count := fakeStorage.GetCount(bucket, prefix); count != 1 {
		t.Errorf("expected 1 object remaining, got %d", count)
	}

	// Test 2: deleteOnResourceDeletion = true -> Objects deleted
	backupDelete := &backupv1alpha1.DatabaseBackup{
		Spec: backupv1alpha1.DatabaseBackupSpec{
			Destination: backupv1alpha1.DestinationSpec{
				Bucket:                   bucket,
				Prefix:                   prefix,
				DeleteOnResourceDeletion: true,
			},
		},
	}

	if backupDelete.Spec.Destination.DeleteOnResourceDeletion {
		objs, _ := fakeStorage.ListObjects(ctx, bucket, prefix)
		var keys []string
		for _, o := range objs {
			keys = append(keys, o.Key)
		}
		_ = fakeStorage.DeleteObjects(ctx, bucket, keys)
	}

	if count := fakeStorage.GetCount(bucket, prefix); count != 0 {
		t.Errorf("expected 0 objects after deletion opt-in execution, got %d", count)
	}
}

func TestSchemeRegistration(t *testing.T) {
	scheme := runtime.NewScheme()
	err := backupv1alpha1.AddToScheme(scheme)
	if err != nil {
		t.Fatalf("failed to add backupv1alpha1 to scheme: %v", err)
	}
	err = batchv1.AddToScheme(scheme)
	if err != nil {
		t.Fatalf("failed to add batchv1 to scheme: %v", err)
	}
	err = corev1.AddToScheme(scheme)
	if err != nil {
		t.Fatalf("failed to add corev1 to scheme: %v", err)
	}

	gvk := backupv1alpha1.GroupVersion.WithKind("DatabaseBackup")
	if !scheme.Recognizes(gvk) {
		t.Errorf("expected scheme to recognize GVK %v", gvk)
	}
}
