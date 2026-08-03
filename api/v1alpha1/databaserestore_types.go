package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RestorePhase defines the phase of a restore operation.
type RestorePhase string

const (
	RestorePhasePending   RestorePhase = "Pending"
	RestorePhaseRunning   RestorePhase = "Running"
	RestorePhaseSucceeded RestorePhase = "Succeeded"
	RestorePhaseFailed    RestorePhase = "Failed"
)

// DatabaseRestoreSpec defines the desired state of DatabaseRestore.
type DatabaseRestoreSpec struct {
	// SourceBackupObjectKey is the full S3 object key for the backup artifact to restore.
	SourceBackupObjectKey string `json:"sourceBackupObjectKey"`
	// TargetDatabase defines the destination database for the restored data.
	TargetDatabase DatabaseSpec `json:"targetDatabase"`
}

// DatabaseRestoreStatus defines the observed state of DatabaseRestore.
type DatabaseRestoreStatus struct {
	// Phase represents the current high-level status of the restore job.
	// +optional
	Phase RestorePhase `json:"phase,omitempty"`
	// Conditions details status conditions for the restore lifecycle.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// StartTime indicates when the restore Job was triggered.
	// +optional
	StartTime *metav1.Time `json:"startTime,omitempty"`
	// CompletionTime indicates when the restore Job completed.
	// +optional
	CompletionTime *metav1.Time `json:"completionTime,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// DatabaseRestore is the Schema for the databaserestores API.
type DatabaseRestore struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DatabaseRestoreSpec   `json:"spec,omitempty"`
	Status DatabaseRestoreStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DatabaseRestoreList contains a list of DatabaseRestore.
type DatabaseRestoreList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DatabaseRestore `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DatabaseRestore{}, &DatabaseRestoreList{})
}
