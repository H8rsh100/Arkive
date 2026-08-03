package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SecretKeySelector selects a key of a Secret.
type SecretKeySelector struct {
	// Name of the Secret.
	Name string `json:"name"`
	// Key for username. Defaults to "username".
	// +optional
	UsernameKey string `json:"usernameKey,omitempty"`
	// Key for password. Defaults to "password".
	// +optional
	PasswordKey string `json:"passwordKey,omitempty"`
}

// DatabaseSpec defines the target database connection settings.
type DatabaseSpec struct {
	// Host of the database server.
	Host string `json:"host"`
	// Port of the database server.
	Port int32 `json:"port"`
	// Name of the database to backup.
	Name string `json:"name"`
	// CredentialsSecret points to the K8s Secret containing credentials.
	CredentialsSecret SecretKeySelector `json:"credentialsSecret"`
}

// RetentionSpec defines the backup retention policy.
type RetentionSpec struct {
	// Count is the maximum number of backup snapshots to retain.
	// +optional
	Count int32 `json:"count,omitempty"`
	// MaxAge specifies the maximum age of backups to keep (e.g. "168h").
	// +optional
	MaxAge string `json:"maxAge,omitempty"`
}

// DestinationSpec defines the target backup storage destination.
type DestinationSpec struct {
	// Type of storage provider (e.g. "s3").
	Type string `json:"type"`
	// Bucket name for S3 storage.
	Bucket string `json:"bucket"`
	// Region for the bucket.
	Region string `json:"region"`
	// Prefix path within the bucket to prevent key collisions.
	Prefix string `json:"prefix"`
	// DeleteOnResourceDeletion specifies whether external S3 backup objects should be deleted when the CRD is deleted.
	// Defaults to false for data safety.
	// +optional
	DeleteOnResourceDeletion bool `json:"deleteOnResourceDeletion,omitempty"`
}

// CronJobRef contains a reference to the created CronJob.
type CronJobRef struct {
	Name string `json:"name"`
}

// DatabaseBackupSpec defines the desired state of DatabaseBackup.
type DatabaseBackupSpec struct {
	// Database connection details.
	Database DatabaseSpec `json:"database"`
	// Schedule in standard Cron format (e.g. "0 2 * * *").
	Schedule string `json:"schedule"`
	// Retention policy for old backups.
	// +optional
	Retention RetentionSpec `json:"retention,omitempty"`
	// Destination storage settings.
	Destination DestinationSpec `json:"destination"`
	// Suspend allows pausing scheduled backups without deleting the CRD.
	// +optional
	Suspend bool `json:"suspend,omitempty"`
}

// DatabaseBackupStatus defines the observed state of DatabaseBackup.
type DatabaseBackupStatus struct {
	// Conditions details the current status of the backup lifecycle.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// ObservedGeneration is the most recent generation observed by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// LastScheduledTime indicates when the backup was last scheduled.
	// +optional
	LastScheduledTime *metav1.Time `json:"lastScheduledTime,omitempty"`
	// LastSuccessfulBackupTime indicates when the last successful backup completed.
	// +optional
	LastSuccessfulBackupTime *metav1.Time `json:"lastSuccessfulBackupTime,omitempty"`
	// LastBackupObjectKey is the S3 object key of the latest successful backup.
	// +optional
	LastBackupObjectKey string `json:"lastBackupObjectKey,omitempty"`
	// ActiveCronJobRef references the managing CronJob object.
	// +optional
	ActiveCronJobRef *CronJobRef `json:"activeCronJobRef,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Schedule",type="string",JSONPath=".spec.schedule"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// DatabaseBackup is the Schema for the databasebackups API.
type DatabaseBackup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DatabaseBackupSpec   `json:"spec,omitempty"`
	Status DatabaseBackupStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DatabaseBackupList contains a list of DatabaseBackup.
type DatabaseBackupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DatabaseBackup `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DatabaseBackup{}, &DatabaseBackupList{})
}
