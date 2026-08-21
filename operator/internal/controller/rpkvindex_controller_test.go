package controller

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	rpkvv1alpha1 "github.com/sonirico/rpkv/operator/api/v1alpha1"
)

// testPartitionSource is a stub partitionSource returning a fixed
// partition count or error.
type testPartitionSource struct {
	partitions int32
	err        error
}

func newTestPartitionSource(partitions int32, err error) *testPartitionSource {
	return &testPartitionSource{partitions: partitions, err: err}
}

func (s *testPartitionSource) Partitions(_ context.Context, _ string) (int32, error) {
	return s.partitions, s.err
}

// testHealthSource is a stub healthSource returning a fixed shape or
// error.
type testHealthSource struct {
	shape topicShape
	err   error
}

func newTestHealthSource(shape topicShape, err error) *testHealthSource {
	return &testHealthSource{shape: shape, err: err}
}

func (s *testHealthSource) Shape(_ context.Context, _, _ string) (topicShape, error) {
	return s.shape, s.err
}

// newTestRpkvIndex builds an RpkvIndex fixture with fields the
// reconciler and workload builders read.
func newTestRpkvIndex() *rpkvv1alpha1.RpkvIndex {
	return &rpkvv1alpha1.RpkvIndex{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "default"},
		Spec: rpkvv1alpha1.RpkvIndexSpec{
			Topic:          "events",
			Brokers:        []string{"broker:9092"},
			Image:          "rpkv:latest",
			ServicePort:    8080,
			RouterReplicas: 1,
			Storage:        rpkvv1alpha1.RpkvIndexStorage{Size: "1Gi"},
		},
	}
}

// newTestScheme registers every type the fake client and reconciler need.
func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, appsv1.AddToScheme(scheme))
	require.NoError(t, rpkvv1alpha1.AddToScheme(scheme))
	return scheme
}

// newTestReconciler wires a fake client seeded with idx into a
// Reconciler backed by the given stub partition source and health
// source.
func newTestReconciler(
	t *testing.T,
	scheme *runtime.Scheme,
	idx *rpkvv1alpha1.RpkvIndex,
	ps partitionSource,
	hs healthSource,
) (*Reconciler, client.Client) {
	t.Helper()

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&rpkvv1alpha1.RpkvIndex{}).
		WithObjects(idx).
		Build()

	factory := func(_ []string) (partitionSource, error) { return ps, nil }
	return NewReconciler(c, scheme, factory, hs), c
}

func TestReconcile(t *testing.T) {
	t.Run("initial reconcile creates the four workloads with owner refs and replicas = partitions", func(t *testing.T) {
		t.Parallel()

		scheme := newTestScheme(t)
		idx := newTestRpkvIndex()
		reconciler, c := newTestReconciler(t, scheme, idx,
			newTestPartitionSource(3, nil),
			newTestHealthSource(topicShape{CleanupPolicy: "delete", PartitionCount: 3}, nil),
		)
		req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(idx)}

		_, err := reconciler.Reconcile(context.Background(), req)
		require.NoError(t, err)

		var sts appsv1.StatefulSet
		require.NoError(t, c.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "demo-shard"}, &sts))
		require.Equal(t, int32(3), *sts.Spec.Replicas)
		require.Len(t, sts.OwnerReferences, 1)
		require.Equal(t, "demo", sts.OwnerReferences[0].Name)

		var shardSvc corev1.Service
		require.NoError(t, c.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "demo-shard"}, &shardSvc))
		require.Len(t, shardSvc.OwnerReferences, 1)

		var router appsv1.Deployment
		require.NoError(t, c.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "demo-router"}, &router))
		require.Len(t, router.OwnerReferences, 1)

		var routerSvc corev1.Service
		require.NoError(t, c.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "demo-router"}, &routerSvc))
		require.Len(t, routerSvc.OwnerReferences, 1)

		var updated rpkvv1alpha1.RpkvIndex
		require.NoError(t, c.Get(context.Background(), req.NamespacedName, &updated))
		require.Equal(t, int32(3), updated.Status.Shards)
		require.Equal(t, int32(3), updated.Status.ObservedPartitions)
	})

	t.Run("partition growth raises replicas", func(t *testing.T) {
		t.Parallel()

		scheme := newTestScheme(t)
		idx := newTestRpkvIndex()
		reconciler, c := newTestReconciler(t, scheme, idx,
			newTestPartitionSource(3, nil),
			newTestHealthSource(topicShape{CleanupPolicy: "delete", PartitionCount: 3}, nil),
		)
		req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(idx)}
		_, err := reconciler.Reconcile(context.Background(), req)
		require.NoError(t, err)

		grown := NewReconciler(c, scheme,
			func(_ []string) (partitionSource, error) { return newTestPartitionSource(5, nil), nil },
			newTestHealthSource(topicShape{CleanupPolicy: "delete", PartitionCount: 5}, nil),
		)
		_, err = grown.Reconcile(context.Background(), req)
		require.NoError(t, err)

		var sts appsv1.StatefulSet
		require.NoError(t, c.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "demo-shard"}, &sts))
		require.Equal(t, int32(5), *sts.Spec.Replicas)
	})

	t.Run("a source reporting fewer partitions never lowers replicas", func(t *testing.T) {
		t.Parallel()

		scheme := newTestScheme(t)
		idx := newTestRpkvIndex()
		reconciler, c := newTestReconciler(t, scheme, idx,
			newTestPartitionSource(5, nil),
			newTestHealthSource(topicShape{CleanupPolicy: "delete", PartitionCount: 5}, nil),
		)
		req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(idx)}
		_, err := reconciler.Reconcile(context.Background(), req)
		require.NoError(t, err)

		shrunk := NewReconciler(c, scheme,
			func(_ []string) (partitionSource, error) { return newTestPartitionSource(2, nil), nil },
			newTestHealthSource(topicShape{CleanupPolicy: "delete", PartitionCount: 2}, nil),
		)
		_, err = shrunk.Reconcile(context.Background(), req)
		require.NoError(t, err)

		var sts appsv1.StatefulSet
		require.NoError(t, c.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "demo-shard"}, &sts))
		require.Equal(t, int32(5), *sts.Spec.Replicas)

		var updated rpkvv1alpha1.RpkvIndex
		require.NoError(t, c.Get(context.Background(), req.NamespacedName, &updated))
		require.Equal(t, int32(5), updated.Status.Shards)
		require.Equal(t, int32(2), updated.Status.ObservedPartitions)
	})

	t.Run("RPKV_SHARDS list matches the replica count", func(t *testing.T) {
		t.Parallel()

		scheme := newTestScheme(t)
		idx := newTestRpkvIndex()
		reconciler, c := newTestReconciler(t, scheme, idx,
			newTestPartitionSource(4, nil),
			newTestHealthSource(topicShape{CleanupPolicy: "delete", PartitionCount: 4}, nil),
		)
		req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(idx)}
		_, err := reconciler.Reconcile(context.Background(), req)
		require.NoError(t, err)

		var router appsv1.Deployment
		require.NoError(t, c.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "demo-router"}, &router))

		var shardsEnv string
		for _, env := range router.Spec.Template.Spec.Containers[0].Env {
			if env.Name == "RPKV_SHARDS" {
				shardsEnv = env.Value
			}
		}
		require.Len(t, strings.Split(shardsEnv, ","), 4)
		require.Contains(t, shardsEnv, "demo-shard-0.demo-shard:8080")
		require.Contains(t, shardsEnv, "demo-shard-3.demo-shard:8080")
	})

	t.Run("CompactionDisabled condition", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name           string
			cleanupPolicy  string
			expectedStatus metav1.ConditionStatus
		}{
			{name: "delete policy disables compaction", cleanupPolicy: "delete", expectedStatus: metav1.ConditionTrue},
			{name: "compact policy leaves compaction enabled", cleanupPolicy: "compact", expectedStatus: metav1.ConditionFalse},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				scheme := newTestScheme(t)
				idx := newTestRpkvIndex()
				reconciler, c := newTestReconciler(t, scheme, idx,
					newTestPartitionSource(3, nil),
					newTestHealthSource(topicShape{CleanupPolicy: tc.cleanupPolicy, PartitionCount: 3}, nil),
				)
				req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(idx)}

				_, err := reconciler.Reconcile(context.Background(), req)
				require.NoError(t, err)

				var updated rpkvv1alpha1.RpkvIndex
				require.NoError(t, c.Get(context.Background(), req.NamespacedName, &updated))
				cond := meta.FindStatusCondition(updated.Status.Conditions, conditionCompactionDisabled)
				require.NotNil(t, cond)
				require.Equal(t, tc.expectedStatus, cond.Status)
			})
		}
	})

	t.Run("Repartitioned becomes True on growth of a compacted topic and stays True on a later clean reconcile", func(t *testing.T) {
		t.Parallel()

		scheme := newTestScheme(t)
		idx := newTestRpkvIndex()
		reconciler, c := newTestReconciler(t, scheme, idx,
			newTestPartitionSource(3, nil),
			newTestHealthSource(topicShape{CleanupPolicy: "compact", PartitionCount: 3}, nil),
		)
		req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(idx)}
		_, err := reconciler.Reconcile(context.Background(), req)
		require.NoError(t, err)

		var afterFirst rpkvv1alpha1.RpkvIndex
		require.NoError(t, c.Get(context.Background(), req.NamespacedName, &afterFirst))
		cond := meta.FindStatusCondition(afterFirst.Status.Conditions, conditionRepartitioned)
		require.NotNil(t, cond)
		require.Equal(t, metav1.ConditionFalse, cond.Status)

		grown := NewReconciler(c, scheme,
			func(_ []string) (partitionSource, error) { return newTestPartitionSource(5, nil), nil },
			newTestHealthSource(topicShape{CleanupPolicy: "compact", PartitionCount: 5}, nil),
		)
		_, err = grown.Reconcile(context.Background(), req)
		require.NoError(t, err)

		var afterGrowth rpkvv1alpha1.RpkvIndex
		require.NoError(t, c.Get(context.Background(), req.NamespacedName, &afterGrowth))
		cond = meta.FindStatusCondition(afterGrowth.Status.Conditions, conditionRepartitioned)
		require.NotNil(t, cond)
		require.Equal(t, metav1.ConditionTrue, cond.Status)

		clean := NewReconciler(c, scheme,
			func(_ []string) (partitionSource, error) { return newTestPartitionSource(5, nil), nil },
			newTestHealthSource(topicShape{CleanupPolicy: "compact", PartitionCount: 5}, nil),
		)
		_, err = clean.Reconcile(context.Background(), req)
		require.NoError(t, err)

		var afterClean rpkvv1alpha1.RpkvIndex
		require.NoError(t, c.Get(context.Background(), req.NamespacedName, &afterClean))
		cond = meta.FindStatusCondition(afterClean.Status.Conditions, conditionRepartitioned)
		require.NotNil(t, cond)
		require.Equal(t, metav1.ConditionTrue, cond.Status)
	})

	t.Run("healthz error yields Unknown conditions", func(t *testing.T) {
		t.Parallel()

		scheme := newTestScheme(t)
		idx := newTestRpkvIndex()
		reconciler, c := newTestReconciler(t, scheme, idx,
			newTestPartitionSource(3, nil),
			newTestHealthSource(topicShape{}, errors.New("healthz unreachable")),
		)
		req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(idx)}

		_, err := reconciler.Reconcile(context.Background(), req)
		require.NoError(t, err)

		var updated rpkvv1alpha1.RpkvIndex
		require.NoError(t, c.Get(context.Background(), req.NamespacedName, &updated))

		compactionCond := meta.FindStatusCondition(updated.Status.Conditions, conditionCompactionDisabled)
		require.NotNil(t, compactionCond)
		require.Equal(t, metav1.ConditionUnknown, compactionCond.Status)
		require.Equal(t, reasonHealthzUnreachable, compactionCond.Reason)

		repartitionedCond := meta.FindStatusCondition(updated.Status.Conditions, conditionRepartitioned)
		require.NotNil(t, repartitionedCond)
		require.Equal(t, metav1.ConditionUnknown, repartitionedCond.Status)
		require.Equal(t, reasonHealthzUnreachable, repartitionedCond.Reason)
	})
}
