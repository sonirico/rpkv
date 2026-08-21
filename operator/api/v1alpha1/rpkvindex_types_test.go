package v1alpha1

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
)

func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	return scheme
}

func TestSchemeBuilderRegistersTypes(t *testing.T) {
	t.Run("registers RpkvIndex and RpkvIndexList GVKs", func(t *testing.T) {
		// Arrange
		scheme := newTestScheme(t)
		indexGVK := GroupVersion.WithKind("RpkvIndex")
		listGVK := GroupVersion.WithKind("RpkvIndexList")

		// Act
		recognizesIndex := scheme.Recognizes(indexGVK)
		recognizesList := scheme.Recognizes(listGVK)

		// Assert
		if !recognizesIndex {
			t.Errorf("expected scheme to recognize %s", indexGVK)
		}
		if !recognizesList {
			t.Errorf("expected scheme to recognize %s", listGVK)
		}
	})
}
