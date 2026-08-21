package v1alpha1

import (
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func newTestRpkvIndexSpec() RpkvIndexSpec {
	return RpkvIndexSpec{
		Topic:          "orders",
		Brokers:        []string{"broker-0:9092", "broker-1:9092"},
		Image:          "rpkv:latest",
		ServicePort:    8080,
		RouterReplicas: 1,
		Storage: RpkvIndexStorage{
			Size:             "5Gi",
			StorageClassName: "fast",
		},
	}
}

func newTestRpkvIndexStatus() RpkvIndexStatus {
	return RpkvIndexStatus{
		Shards:             3,
		ObservedPartitions: 12,
		Conditions: []metav1.Condition{
			{Type: "CompactionDisabled", Status: metav1.ConditionTrue, Reason: "Idle"},
		},
	}
}

func newTestRpkvIndex() *RpkvIndex {
	return &RpkvIndex{
		ObjectMeta: metav1.ObjectMeta{Name: "orders-index", Namespace: "default"},
		Spec:       newTestRpkvIndexSpec(),
		Status:     newTestRpkvIndexStatus(),
	}
}

func newTestRpkvIndexList() *RpkvIndexList {
	return &RpkvIndexList{
		Items: []RpkvIndex{*newTestRpkvIndex()},
	}
}

func TestDeepCopy(t *testing.T) {
	t.Run("RpkvIndexStorage", func(t *testing.T) {
		// Arrange
		original := &RpkvIndexStorage{Size: "5Gi", StorageClassName: "fast"}

		// Act
		copied := original.DeepCopy()
		copied.Size = "10Gi"

		// Assert
		if original.Size == "10Gi" {
			t.Fatal("mutating the copy affected the original")
		}
	})

	t.Run("RpkvIndexSpec", func(t *testing.T) {
		// Arrange
		original := newTestRpkvIndexSpec()

		// Act
		copied := original.DeepCopy()
		if !reflect.DeepEqual(&original, copied) {
			t.Fatal("expected copy to equal original before mutation")
		}
		copied.Brokers[0] = "mutated:9092"

		// Assert
		if original.Brokers[0] == "mutated:9092" {
			t.Fatal("mutating the copy's Brokers slice affected the original")
		}
	})

	t.Run("RpkvIndexStatus", func(t *testing.T) {
		// Arrange
		original := newTestRpkvIndexStatus()

		// Act
		copied := original.DeepCopy()
		if !reflect.DeepEqual(&original, copied) {
			t.Fatal("expected copy to equal original before mutation")
		}
		copied.Conditions[0].Reason = "Mutated"

		// Assert
		if original.Conditions[0].Reason == "Mutated" {
			t.Fatal("mutating the copy's Conditions slice affected the original")
		}
	})

	t.Run("RpkvIndex DeepCopy", func(t *testing.T) {
		// Arrange
		original := newTestRpkvIndex()

		// Act
		copied := original.DeepCopy()
		if !reflect.DeepEqual(original, copied) {
			t.Fatal("expected copy to equal original before mutation")
		}
		copied.Spec.Brokers[0] = "mutated:9092"
		copied.Status.Conditions[0].Reason = "Mutated"

		// Assert
		if original.Spec.Brokers[0] == "mutated:9092" {
			t.Fatal("mutating the copy's Spec affected the original")
		}
		if original.Status.Conditions[0].Reason == "Mutated" {
			t.Fatal("mutating the copy's Status affected the original")
		}
	})

	t.Run("RpkvIndex DeepCopyInto", func(t *testing.T) {
		// Arrange
		original := newTestRpkvIndex()
		var dst RpkvIndex

		// Act
		original.DeepCopyInto(&dst)
		if !reflect.DeepEqual(original, &dst) {
			t.Fatal("expected DeepCopyInto target to equal original before mutation")
		}
		dst.Spec.Brokers[0] = "mutated:9092"

		// Assert
		if original.Spec.Brokers[0] == "mutated:9092" {
			t.Fatal("mutating the DeepCopyInto target affected the original")
		}
	})

	t.Run("RpkvIndex DeepCopyObject", func(t *testing.T) {
		// Arrange
		original := newTestRpkvIndex()

		// Act
		obj := original.DeepCopyObject()

		// Assert
		copied, ok := obj.(*RpkvIndex)
		if !ok {
			t.Fatalf("expected DeepCopyObject to return *RpkvIndex, got %T", obj)
		}
		if !reflect.DeepEqual(original, copied) {
			t.Fatal("expected DeepCopyObject result to equal original")
		}
	})

	t.Run("RpkvIndexList DeepCopy", func(t *testing.T) {
		// Arrange
		original := newTestRpkvIndexList()

		// Act
		copied := original.DeepCopy()
		if !reflect.DeepEqual(original, copied) {
			t.Fatal("expected copy to equal original before mutation")
		}
		copied.Items[0].Spec.Brokers[0] = "mutated:9092"

		// Assert
		if original.Items[0].Spec.Brokers[0] == "mutated:9092" {
			t.Fatal("mutating the copy's Items affected the original")
		}
	})

	t.Run("RpkvIndexList DeepCopyInto", func(t *testing.T) {
		// Arrange
		original := newTestRpkvIndexList()
		var dst RpkvIndexList

		// Act
		original.DeepCopyInto(&dst)
		if !reflect.DeepEqual(original, &dst) {
			t.Fatal("expected DeepCopyInto target to equal original before mutation")
		}
		dst.Items[0].Spec.Brokers[0] = "mutated:9092"

		// Assert
		if original.Items[0].Spec.Brokers[0] == "mutated:9092" {
			t.Fatal("mutating the DeepCopyInto target affected the original")
		}
	})

	t.Run("RpkvIndexList DeepCopyObject", func(t *testing.T) {
		// Arrange
		original := newTestRpkvIndexList()

		// Act
		obj := original.DeepCopyObject()

		// Assert
		copied, ok := obj.(*RpkvIndexList)
		if !ok {
			t.Fatalf("expected DeepCopyObject to return *RpkvIndexList, got %T", obj)
		}
		if !reflect.DeepEqual(original, copied) {
			t.Fatal("expected DeepCopyObject result to equal original")
		}
	})

	t.Run("nil receivers return nil", func(t *testing.T) {
		// Arrange
		var nilIndex *RpkvIndex
		var nilList *RpkvIndexList
		var nilSpec *RpkvIndexSpec
		var nilStatus *RpkvIndexStatus
		var nilStorage *RpkvIndexStorage

		// Act
		indexCopy := nilIndex.DeepCopy()
		listCopy := nilList.DeepCopy()
		specCopy := nilSpec.DeepCopy()
		statusCopy := nilStatus.DeepCopy()
		storageCopy := nilStorage.DeepCopy()

		// Assert
		if indexCopy != nil {
			t.Errorf("expected nil *RpkvIndex.DeepCopy() to return nil, got %v", indexCopy)
		}
		if listCopy != nil {
			t.Errorf("expected nil *RpkvIndexList.DeepCopy() to return nil, got %v", listCopy)
		}
		if specCopy != nil {
			t.Errorf("expected nil *RpkvIndexSpec.DeepCopy() to return nil, got %v", specCopy)
		}
		if statusCopy != nil {
			t.Errorf("expected nil *RpkvIndexStatus.DeepCopy() to return nil, got %v", statusCopy)
		}
		if storageCopy != nil {
			t.Errorf("expected nil *RpkvIndexStorage.DeepCopy() to return nil, got %v", storageCopy)
		}
	})
}
