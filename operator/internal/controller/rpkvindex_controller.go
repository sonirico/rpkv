// Package controller reconciles RpkvIndex objects into shard and router
// workloads, growing shard count with the indexed topic's partition
// count and surfacing compaction and repartition health from the
// running rpkv processes.
package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	rpkvv1alpha1 "github.com/sonirico/rpkv/operator/api/v1alpha1"
)

const (
	conditionCompactionDisabled = "CompactionDisabled"
	conditionRepartitioned      = "Repartitioned"

	reasonHealthzUnreachable        = "HealthzUnreachable"
	reasonHealthzObserved           = "HealthzObserved"
	reasonPartitionGrowthObserved   = "PartitionGrowthObserved"
	reasonNoPartitionGrowthObserved = "NoPartitionGrowthObserved"

	requeueInterval = 30 * time.Second
)

// partitionSourceFactory builds a partitionSource over the given seed
// brokers.
type partitionSourceFactory func(brokers []string) (partitionSource, error)

// Reconciler reconciles a RpkvIndex object into its shard and router
// workloads.
type Reconciler struct {
	client.Client
	Scheme *runtime.Scheme

	partitionsFor partitionSourceFactory
	health        healthSource
}

// NewReconciler wires an already-configured client, scheme, partition
// source factory and health source into a Reconciler.
func NewReconciler(
	c client.Client,
	scheme *runtime.Scheme,
	partitionsFor partitionSourceFactory,
	health healthSource,
) *Reconciler {
	return &Reconciler{
		Client:        c,
		Scheme:        scheme,
		partitionsFor: partitionsFor,
		health:        health,
	}
}

// Reconcile grows the RpkvIndex's shard count to match its topic's
// partition count, applies the shard and router workloads, and refreshes
// the CompactionDisabled and Repartitioned status conditions from the
// running shard's /healthz endpoint.
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := logf.FromContext(ctx)

	var idx rpkvv1alpha1.RpkvIndex
	if err := r.Get(ctx, req.NamespacedName, &idx); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("rpkvindex: get %s: %w", req.NamespacedName, err)
	}

	source, err := r.partitionsFor(idx.Spec.Brokers)
	if err != nil {
		logger.Error(err, "rpkvindex: build partition source", "name", req.NamespacedName)
		return ctrl.Result{RequeueAfter: requeueInterval}, nil
	}

	partitions, err := source.Partitions(ctx, idx.Spec.Topic)
	if err != nil {
		logger.Error(err, "rpkvindex: read partitions", "name", req.NamespacedName, "topic", idx.Spec.Topic)
		return ctrl.Result{RequeueAfter: requeueInterval}, nil
	}

	shards := idx.Status.Shards
	if partitions > shards {
		shards = partitions
	}
	if shards < 1 {
		shards = 1
	}

	if err := r.applyWorkloads(ctx, &idx, shards); err != nil {
		return ctrl.Result{}, fmt.Errorf("rpkvindex: apply workloads %s: %w", req.NamespacedName, err)
	}

	r.refreshHealthConditions(ctx, &idx)

	idx.Status.Shards = shards
	idx.Status.ObservedPartitions = partitions
	if err := r.Status().Update(ctx, &idx); err != nil {
		return ctrl.Result{}, fmt.Errorf("rpkvindex: update status %s: %w", req.NamespacedName, err)
	}

	return ctrl.Result{RequeueAfter: requeueInterval}, nil
}

// applyWorkloads creates or updates the shard StatefulSet, shard headless
// Service, router Deployment and router Service, each owned by idx.
func (r *Reconciler) applyWorkloads(ctx context.Context, idx *rpkvv1alpha1.RpkvIndex, shards int32) error {
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: idx.Name + "-shard", Namespace: idx.Namespace},
	}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, sts, func() error {
		desired := newShardStatefulSet(idx, shards)
		sts.Labels = desired.Labels
		sts.Spec = desired.Spec
		return controllerutil.SetControllerReference(idx, sts, r.Scheme)
	}); err != nil {
		return fmt.Errorf("shard statefulset: %w", err)
	}

	shardSvc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: idx.Name + "-shard", Namespace: idx.Namespace},
	}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, shardSvc, func() error {
		desired := newShardService(idx)
		shardSvc.Labels = desired.Labels
		shardSvc.Spec = desired.Spec
		return controllerutil.SetControllerReference(idx, shardSvc, r.Scheme)
	}); err != nil {
		return fmt.Errorf("shard service: %w", err)
	}

	router := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: idx.Name + "-router", Namespace: idx.Namespace},
	}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, router, func() error {
		desired := newRouterDeployment(idx, shards)
		router.Labels = desired.Labels
		router.Spec = desired.Spec
		return controllerutil.SetControllerReference(idx, router, r.Scheme)
	}); err != nil {
		return fmt.Errorf("router deployment: %w", err)
	}

	routerSvc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: idx.Name + "-router", Namespace: idx.Namespace},
	}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, routerSvc, func() error {
		desired := newRouterService(idx)
		routerSvc.Labels = desired.Labels
		routerSvc.Spec = desired.Spec
		return controllerutil.SetControllerReference(idx, routerSvc, r.Scheme)
	}); err != nil {
		return fmt.Errorf("router service: %w", err)
	}

	return nil
}

// refreshHealthConditions reads the running shard-0's /healthz endpoint
// and updates idx's CompactionDisabled and Repartitioned conditions.
// Repartitioned is sticky: once True it is never set back to False,
// including when healthz cannot be reached.
func (r *Reconciler) refreshHealthConditions(ctx context.Context, idx *rpkvv1alpha1.RpkvIndex) {
	baseURL := fmt.Sprintf(
		"http://%s-shard-0.%s-shard.%s.svc:%d",
		idx.Name, idx.Name, idx.Namespace, idx.Spec.ServicePort,
	)

	shape, err := r.health.Shape(ctx, baseURL, idx.Spec.Topic)
	if err != nil {
		logf.FromContext(ctx).Error(err, "rpkvindex: read healthz", "name", idx.Name, "namespace", idx.Namespace)
		setHealthzUnreachable(idx)
		return
	}

	compacted := strings.Contains(shape.CleanupPolicy, "compact")

	compactionDisabledStatus := metav1.ConditionFalse
	if !compacted {
		compactionDisabledStatus = metav1.ConditionTrue
	}
	meta.SetStatusCondition(&idx.Status.Conditions, metav1.Condition{
		Type:    conditionCompactionDisabled,
		Status:  compactionDisabledStatus,
		Reason:  reasonHealthzObserved,
		Message: fmt.Sprintf("cleanup.policy=%q", shape.CleanupPolicy),
	})

	grew := compacted && shape.PartitionCount > idx.Status.ObservedPartitions && idx.Status.ObservedPartitions > 0
	if grew {
		meta.SetStatusCondition(&idx.Status.Conditions, metav1.Condition{
			Type:   conditionRepartitioned,
			Status: metav1.ConditionTrue,
			Reason: reasonPartitionGrowthObserved,
			Message: fmt.Sprintf(
				"partition_count grew from %d to %d while compacted",
				idx.Status.ObservedPartitions, shape.PartitionCount,
			),
		})
		return
	}

	if repartitionedTrue(idx) {
		return
	}
	meta.SetStatusCondition(&idx.Status.Conditions, metav1.Condition{
		Type:   conditionRepartitioned,
		Status: metav1.ConditionFalse,
		Reason: reasonNoPartitionGrowthObserved,
	})
}

// setHealthzUnreachable marks CompactionDisabled Unknown, and
// Repartitioned Unknown unless it is already sticky True.
func setHealthzUnreachable(idx *rpkvv1alpha1.RpkvIndex) {
	meta.SetStatusCondition(&idx.Status.Conditions, metav1.Condition{
		Type:   conditionCompactionDisabled,
		Status: metav1.ConditionUnknown,
		Reason: reasonHealthzUnreachable,
	})

	if repartitionedTrue(idx) {
		return
	}
	meta.SetStatusCondition(&idx.Status.Conditions, metav1.Condition{
		Type:   conditionRepartitioned,
		Status: metav1.ConditionUnknown,
		Reason: reasonHealthzUnreachable,
	})
}

// repartitionedTrue reports whether idx's Repartitioned condition is
// already sticky True.
func repartitionedTrue(idx *rpkvv1alpha1.RpkvIndex) bool {
	c := meta.FindStatusCondition(idx.Status.Conditions, conditionRepartitioned)
	return c != nil && c.Status == metav1.ConditionTrue
}

// SetupWithManager registers the Reconciler with mgr, watching RpkvIndex
// objects and the workloads it owns.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&rpkvv1alpha1.RpkvIndex{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Complete(r)
}
