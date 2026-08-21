package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RpkvIndexSpec defines the desired state of one indexed topic.
type RpkvIndexSpec struct {
	// Topic is the indexed topic; the operator watches its partition
	// count over the Kafka protocol.
	Topic string `json:"topic"`
	// Brokers are the seed broker addresses.
	Brokers []string `json:"brokers"`
	// Image is the rpkv container image for shards and router.
	Image string `json:"image"`
	// +kubebuilder:default=8080
	ServicePort int32 `json:"servicePort,omitempty"`
	// +kubebuilder:default=1
	RouterReplicas int32            `json:"routerReplicas,omitempty"`
	Storage        RpkvIndexStorage `json:"storage,omitempty"`
}

// RpkvIndexStorage sizes each shard's PersistentVolumeClaim.
type RpkvIndexStorage struct {
	// +kubebuilder:default="1Gi"
	Size             string `json:"size,omitempty"`
	StorageClassName string `json:"storageClassName,omitempty"`
}

// RpkvIndexStatus reports what the operator observed.
type RpkvIndexStatus struct {
	// Shards is the shard StatefulSet's current desired replicas.
	Shards int32 `json:"shards,omitempty"`
	// ObservedPartitions is the topic partition count last read over
	// the Kafka protocol.
	ObservedPartitions int32 `json:"observedPartitions,omitempty"`
	// Conditions surface what the rpkv process itself reports:
	// CompactionDisabled and Repartitioned.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// RpkvIndex is the Schema for the rpkvindices API.
type RpkvIndex struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RpkvIndexSpec   `json:"spec,omitempty"`
	Status RpkvIndexStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// RpkvIndexList contains a list of RpkvIndex.
type RpkvIndexList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RpkvIndex `json:"items"`
}

func init() {
	SchemeBuilder.Register(&RpkvIndex{}, &RpkvIndexList{})
}
