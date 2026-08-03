package controllers

import (
	"context"
	"fmt"
	"reflect"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	backupv1alpha1 "github.com/H8rsh100/arkive/api/v1alpha1"
	"github.com/H8rsh100/arkive/internal/storage"
)

const arkiveFinalizer = "backup.arkive.io/finalizer"

// DatabaseBackupReconciler reconciles a DatabaseBackup object.
type DatabaseBackupReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	StorageClient storage.ObjectStorageClient
}

// +kubebuilder:rbac:groups=backup.arkive.io,resources=databasebackups,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=backup.arkive.io,resources=databasebackups/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=backup.arkive.io,resources=databasebackups/finalizers,verbs=update
// +kubebuilder:rbac:groups=batch,resources=cronjobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

func (r *DatabaseBackupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var backup backupv1alpha1.DatabaseBackup
	if err := r.Get(ctx, req.NamespacedName, &backup); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// 1. Handle deletion via finalizer BEFORE any create/update logic
	if !backup.ObjectMeta.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, &backup)
	}

	if !controllerutil.ContainsFinalizer(&backup, arkiveFinalizer) {
		controllerutil.AddFinalizer(&backup, arkiveFinalizer)
		if err := r.Update(ctx, &backup); err != nil {
			return ctrl.Result{}, err
		}
	}

	// 2. Validate spec — fail fast with condition update
	if err := r.validateSpec(ctx, &backup); err != nil {
		logger.Error(err, "invalid DatabaseBackup spec", "name", backup.Name)
		r.setCondition(&backup, "Ready", metav1.ConditionFalse, "InvalidSpec", err.Error())
		_ = r.Status().Update(ctx, &backup)
		// User input error: return nil to avoid tight retry loop
		return ctrl.Result{}, nil
	}

	// 3. Reconcile the CronJob (create-or-update)
	desiredCronJob := r.buildCronJob(&backup)
	var existingCronJob batchv1.CronJob
	cronJobKey := types.NamespacedName{Name: desiredCronJob.Name, Namespace: backup.Namespace}
	err := r.Get(ctx, cronJobKey, &existingCronJob)

	switch {
	case errors.IsNotFound(err):
		if err := controllerutil.SetControllerReference(&backup, desiredCronJob, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}
		logger.Info("creating backing CronJob", "cronjob", desiredCronJob.Name)
		if err := r.Create(ctx, desiredCronJob); err != nil {
			return ctrl.Result{}, err
		}
	case err != nil:
		return ctrl.Result{}, err
	default:
		if !r.cronJobSpecEqual(&existingCronJob.Spec, &desiredCronJob.Spec) {
			logger.Info("updating backing CronJob spec", "cronjob", existingCronJob.Name)
			existingCronJob.Spec = desiredCronJob.Spec
			if err := r.Update(ctx, &existingCronJob); err != nil {
				return ctrl.Result{}, err
			}
		}
	}

	// 4. Retention cleanup (if storage client provided)
	if r.StorageClient != nil && backup.Spec.Retention.Count > 0 {
		if err := r.enforceRetention(ctx, &backup); err != nil {
			logger.Error(err, "retention enforcement failed")
			r.setCondition(&backup, "Degraded", metav1.ConditionTrue, "RetentionCleanupFailed", err.Error())
		} else {
			r.setCondition(&backup, "Degraded", metav1.ConditionFalse, "RetentionSuccess", "Retention policy cleanly enforced")
		}
	}

	// 5. Update status
	backup.Status.ObservedGeneration = backup.Generation
	backup.Status.ActiveCronJobRef = &backupv1alpha1.CronJobRef{Name: desiredCronJob.Name}
	r.setCondition(&backup, "Ready", metav1.ConditionTrue, "Reconciled", "CronJob reconciled and active")

	if err := r.Status().Update(ctx, &backup); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
}

func (r *DatabaseBackupReconciler) handleDeletion(ctx context.Context, backup *backupv1alpha1.DatabaseBackup) (ctrl.Result, error) {
	if controllerutil.ContainsFinalizer(backup, arkiveFinalizer) {
		// If explicit opt-in deleteOnResourceDeletion is true, cleanup S3 objects
		if backup.Spec.Destination.DeleteOnResourceDeletion && r.StorageClient != nil {
			objs, err := r.StorageClient.ListObjects(ctx, backup.Spec.Destination.Bucket, backup.Spec.Destination.Prefix)
			if err == nil && len(objs) > 0 {
				var keys []string
				for _, o := range objs {
					keys = append(keys, o.Key)
				}
				if err := r.StorageClient.DeleteObjects(ctx, backup.Spec.Destination.Bucket, keys); err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to delete S3 backup objects during finalization: %w", err)
				}
			}
		}

		controllerutil.RemoveFinalizer(backup, arkiveFinalizer)
		if err := r.Update(ctx, backup); err != nil {
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{}, nil
}

func (r *DatabaseBackupReconciler) validateSpec(ctx context.Context, backup *backupv1alpha1.DatabaseBackup) error {
	if backup.Spec.Database.Host == "" {
		return fmt.Errorf("database host cannot be empty")
	}
	if backup.Spec.Database.Name == "" {
		return fmt.Errorf("database name cannot be empty")
	}
	if backup.Spec.Schedule == "" {
		return fmt.Errorf("backup schedule cannot be empty")
	}
	if backup.Spec.Destination.Bucket == "" {
		return fmt.Errorf("destination bucket cannot be empty")
	}
	if backup.Spec.Database.CredentialsSecret.Name == "" {
		return fmt.Errorf("credentials secret name cannot be empty")
	}
	return nil
}

func (r *DatabaseBackupReconciler) enforceRetention(ctx context.Context, backup *backupv1alpha1.DatabaseBackup) error {
	bucket := backup.Spec.Destination.Bucket
	prefix := backup.Spec.Destination.Prefix
	maxCount := int(backup.Spec.Retention.Count)

	objs, err := r.StorageClient.ListObjects(ctx, bucket, prefix)
	if err != nil {
		return err
	}

	if len(objs) <= maxCount {
		return nil
	}

	excessCount := len(objs) - maxCount
	var keysToDelete []string
	for i := 0; i < excessCount; i++ {
		keysToDelete = append(keysToDelete, objs[i].Key)
	}

	return r.StorageClient.DeleteObjects(ctx, bucket, keysToDelete)
}

func (r *DatabaseBackupReconciler) buildCronJob(backup *backupv1alpha1.DatabaseBackup) *batchv1.CronJob {
	cronName := backup.Name + "-cronjob"
	usernameKey := backup.Spec.Database.CredentialsSecret.UsernameKey
	if usernameKey == "" {
		usernameKey = "username"
	}
	passwordKey := backup.Spec.Database.CredentialsSecret.PasswordKey
	if passwordKey == "" {
		passwordKey = "password"
	}

	cmd := fmt.Sprintf("PGPASSWORD=$DB_PASS pg_dump -h %s -p %d -U $DB_USER -d %s | gzip > /tmp/dump.sql.gz",
		backup.Spec.Database.Host,
		backup.Spec.Database.Port,
		backup.Spec.Database.Name,
	)

	return &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cronName,
			Namespace: backup.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "arkive-operator",
				"arkive.io/backup-owner":       backup.Name,
			},
		},
		Spec: batchv1.CronJobSpec{
			Schedule: backup.Spec.Schedule,
			Suspend:  &backup.Spec.Suspend,
			JobTemplate: batchv1.JobTemplateSpec{
				Spec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							RestartPolicy: corev1.RestartPolicyOnFailure,
							Containers: []corev1.Container{
								{
									Name:    "backup-runner",
									Image:   "postgres:16-alpine",
									Command: []string{"/bin/sh", "-c", cmd},
									Env: []corev1.EnvVar{
										{
											Name: "DB_USER",
											ValueFrom: &corev1.EnvVarSource{
												SecretKeyRef: &corev1.SecretKeySelector{
													LocalObjectReference: corev1.LocalObjectReference{
														Name: backup.Spec.Database.CredentialsSecret.Name,
													},
													Key: usernameKey,
												},
											},
										},
										{
											Name: "DB_PASS",
											ValueFrom: &corev1.EnvVarSource{
												SecretKeyRef: &corev1.SecretKeySelector{
													LocalObjectReference: corev1.LocalObjectReference{
														Name: backup.Spec.Database.CredentialsSecret.Name,
													},
													Key: passwordKey,
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

func (r *DatabaseBackupReconciler) cronJobSpecEqual(existing, desired *batchv1.CronJobSpec) bool {
	if existing.Schedule != desired.Schedule {
		return false
	}
	if (existing.Suspend == nil && desired.Suspend != nil) ||
		(existing.Suspend != nil && desired.Suspend != nil && *existing.Suspend != *desired.Suspend) {
		return false
	}
	return reflect.DeepEqual(existing.JobTemplate.Spec.Template.Spec.Containers, desired.JobTemplate.Spec.Template.Spec.Containers)
}

func (r *DatabaseBackupReconciler) setCondition(backup *backupv1alpha1.DatabaseBackup, condType string, status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&backup.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: backup.Generation,
	})
}

// SetupWithManager sets up the controller with the Manager.
func (r *DatabaseBackupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&backupv1alpha1.DatabaseBackup{}).
		Owns(&batchv1.CronJob{}).
		Complete(r)
}
