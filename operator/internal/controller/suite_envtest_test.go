package controller

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	rpkvv1alpha1 "github.com/sonirico/rpkv/operator/api/v1alpha1"
)

const envtestPollBound = 30 * time.Second

// touchEnvtestCR patches idx with a fresh annotation so the manager's
// watch on RpkvIndex fires a reconcile without waiting out the
// reconciler's periodic requeue interval.
func touchEnvtestCR(t *testing.T, ctx context.Context, c client.Client, idx *rpkvv1alpha1.RpkvIndex) {
	t.Helper()

	var cur rpkvv1alpha1.RpkvIndex
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(idx), &cur))
	if cur.Annotations == nil {
		cur.Annotations = map[string]string{}
	}
	cur.Annotations["rpkv.sonirico.dev/touch"] = time.Now().Format(time.RFC3339Nano)
	require.NoError(t, c.Update(ctx, &cur))
}

// TestEnvtest boots a real kube-apiserver via envtest, starts the
// Reconciler against it with stub partition and health sources, and
// exercises the grow-only sharding contract end to end. Guarded at
// runtime by RPKV_ENVTEST=1 rather than a build tag, since this file
// shares the fixtures defined in rpkvindex_controller_test.go.
func TestEnvtest(t *testing.T) {
	if os.Getenv("RPKV_ENVTEST") != "1" {
		t.Skip("RPKV_ENVTEST=1 not set; skipping envtest reconcile suite")
	}

	logf.SetLogger(zap.New(zap.WriteTo(os.Stderr)))

	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}

	cfg, err := testEnv.Start()
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, testEnv.Stop())
	})

	scheme := newTestScheme(t)

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
	})
	require.NoError(t, err)

	ps := newTestPartitionSource(3, nil)
	hs := newTestHealthSource(topicShape{CleanupPolicy: "delete", PartitionCount: 3}, nil)

	reconciler := NewReconciler(
		mgr.GetClient(),
		mgr.GetScheme(),
		func(_ []string) (partitionSource, error) { return ps, nil },
		hs,
	)
	require.NoError(t, reconciler.SetupWithManager(mgr))

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	mgrDone := make(chan error, 1)
	go func() { mgrDone <- mgr.Start(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-mgrDone
	})

	require.True(t, mgr.GetCache().WaitForCacheSync(ctx))

	c := mgr.GetClient()
	idx := newTestRpkvIndex()
	require.NoError(t, c.Create(ctx, idx))

	shardKey := client.ObjectKey{Namespace: idx.Namespace, Name: idx.Name + "-shard"}

	t.Run("applying the CR yields the shard StatefulSet with replicas = the stub's partitions", func(t *testing.T) {
		var sts appsv1.StatefulSet
		require.Eventually(t, func() bool {
			if err := c.Get(ctx, shardKey, &sts); err != nil {
				return false
			}
			return sts.Spec.Replicas != nil && *sts.Spec.Replicas == 3
		}, envtestPollBound, 200*time.Millisecond)
	})

	t.Run("raising the stub's partitions and waiting yields grown replicas", func(t *testing.T) {
		ps.SetPartitions(5)
		touchEnvtestCR(t, ctx, c, idx)

		var sts appsv1.StatefulSet
		require.Eventually(t, func() bool {
			if err := c.Get(ctx, shardKey, &sts); err != nil {
				return false
			}
			return sts.Spec.Replicas != nil && *sts.Spec.Replicas == 5
		}, envtestPollBound, 200*time.Millisecond)
	})

	t.Run("lowering the stub's partitions never shrinks replicas", func(t *testing.T) {
		ps.SetPartitions(2)
		touchEnvtestCR(t, ctx, c, idx)

		require.Eventually(t, func() bool {
			var cur rpkvv1alpha1.RpkvIndex
			if err := c.Get(ctx, client.ObjectKeyFromObject(idx), &cur); err != nil {
				return false
			}
			return cur.Status.ObservedPartitions == 2
		}, envtestPollBound, 200*time.Millisecond)

		var sts appsv1.StatefulSet
		require.NoError(t, c.Get(ctx, shardKey, &sts))
		require.Equal(t, int32(5), *sts.Spec.Replicas)
	})
}
