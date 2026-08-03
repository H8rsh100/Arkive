package controllers

import (
	"context"
	"fmt"

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
)

// DatabaseRestoreReconciler reconciles a DatabaseRestore object.
type DatabaseRestoreReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=backup.arkive.io,resources=databaserestores,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=backup.arkive.io,resources=databaserestores/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete

func (r *DatabaseRestoreReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var restore backupv1alpha1.DatabaseRestore
	if err := r.Get(ctx, req.NamespacedName, &restore); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// 1. Validate spec
	if restore.Spec.SourceBackupObjectKey == "" || restore.Spec.TargetDatabase.Host == "" {
		restore.Status.Phase = backupv1alpha1.RestorePhaseFailed
		r.setCondition(&restore, "Ready", metav1.ConditionFalse, "InvalidSpec", "sourceBackupObjectKey and targetDatabase are required")
		_ = r.Status().Update(ctx, &restore)
		return ctrl.Result{}, nil
	}

	// 2. Reconcile backing restore Job
	jobName := restore.Name + "-job"
	jobKey := types.NamespacedName{Name: jobName, Namespace: restore.Namespace}
	var existingJob batchv1.Job
	err := r.Get(ctx, jobKey, &existingJob)

	now := metav1.Now()

	switch {
	case errors.IsNotFound(err):
		desiredJob := r.buildRestoreJob(&restore)
		if err := controllerutil.SetControllerReference(&restore, desiredJob, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}

		logger.Info("triggering restore job", "job", desiredJob.Name)
		if err := r.Create(ctx, desiredJob); err != nil {
			return ctrl.Result{}, err
		}

		restore.Status.Phase = backupv1alpha1.RestorePhasePending
		restore.Status.StartTime = &now
		r.setCondition(&restore, "Ready", metav1.ConditionFalse, "JobCreated", "Restore Job created, execution pending")

	case err != nil:
		return ctrl.Result{}, err

	default:
		if existingJob.Status.Succeeded > 0 {
			restore.Status.Phase = backupv1alpha1.RestorePhaseSucceeded
			if restore.Status.CompletionTime == nil {
				restore.Status.CompletionTime = &now
			}
			r.setCondition(&restore, "Ready", metav1.ConditionTrue, "RestoreSucceeded", "Database restore completed successfully")
		} else if existingJob.Status.Failed > 0 {
			restore.Status.Phase = backupv1alpha1.RestorePhaseFailed
			if restore.Status.CompletionTime == nil {
				restore.Status.CompletionTime = &now
			}
			r.setCondition(&restore, "Ready", metav1.ConditionFalse, "RestoreFailed", "Database restore Job failed")
		} else {
			restore.Status.Phase = backupv1alpha1.RestorePhaseRunning
			r.setCondition(&restore, "Ready", metav1.ConditionFalse, "RestoreRunning", "Database restore Job is currently running")
		}
	}

	if err := r.Status().Update(ctx, &restore); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *DatabaseRestoreReconciler) buildRestoreJob(restore *backupv1alpha1.DatabaseRestore) *batchv1.Job {
	jobName := restore.Name + "-job"
	usernameKey := restore.Spec.TargetDatabase.CredentialsSecret.UsernameKey
	if usernameKey == "" {
		usernameKey = "username"
	}
	passwordKey := restore.Spec.TargetDatabase.CredentialsSecret.PasswordKey
	if passwordKey == "" {
		passwordKey = "password"
	}

	cmd := fmt.Sprintf("PGPASSWORD=$DB_PASS psql -h %s -p %d -U $DB_USER -d %s < /tmp/backup.sql",
		restore.Spec.TargetDatabase.Host,
		restore.Spec.TargetDatabase.Port,
		restore.Spec.TargetDatabase.Name,
	)

	backoffLimit := int32(2)

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: restore.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "arkive-operator",
				"arkive.io/restore-owner":      restore.Name,
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: &backoffLimit,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{
						{
							Name:    "restore-runner",
							Image:   "postgres:16-alpine",
							Command: []string{"/bin/sh", "-c", cmd},
							Env: []corev1.EnvVar{
								{
									Name:  "SOURCE_KEY",
									Value: restore.Spec.SourceBackupObjectKey,
								},
								{
									Name: "DB_USER",
									ValueFrom: &corev1.EnvVarSource{
										SecretKeyRef: &corev1.SecretKeySelector{
											LocalObjectReference: corev1.LocalObjectReference{
												Name: restore.Spec.TargetDatabase.CredentialsSecret.Name,
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
												Name: restore.Spec.TargetDatabase.CredentialsSecret.Name,
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
	}
}

func (r *DatabaseRestoreReconciler) setCondition(restore *backupv1alpha1.DatabaseRestore, condType string, status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&restore.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: restore.Generation,
	})
}

// SetupWithManager sets up the controller with the Manager.
func (r *DatabaseRestoreReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&backupv1alpha1.DatabaseRestore{}).
		Owns(&batchv1.Job{}).
		Complete(r)
}
