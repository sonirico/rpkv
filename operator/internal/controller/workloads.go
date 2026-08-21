package controller

import (
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	rpkvv1alpha1 "github.com/sonirico/rpkv/operator/api/v1alpha1"
)

const (
	managedByOperator = "rpkv-operator"
	componentShard    = "shard"
	componentRouter   = "router"
)

// workloadLabels are the labels attached to every object the operator
// owns for idx, and the selector both the StatefulSet and Deployment
// match their pods on.
func workloadLabels(idx *rpkvv1alpha1.RpkvIndex, component string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "rpkv",
		"app.kubernetes.io/instance":   idx.Name,
		"app.kubernetes.io/component":  component,
		"app.kubernetes.io/managed-by": managedByOperator,
	}
}

// healthzProbe checks the named http container port's /healthz endpoint.
func healthzProbe() *corev1.Probe {
	return &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{
			HTTPGet: &corev1.HTTPGetAction{
				Path: "/healthz",
				Port: intstr.FromString("http"),
			},
		},
	}
}

// newShardStatefulSet builds the desired shard StatefulSet for idx,
// sized to shards replicas.
func newShardStatefulSet(idx *rpkvv1alpha1.RpkvIndex, shards int32) *appsv1.StatefulSet {
	labels := workloadLabels(idx, componentShard)
	name := idx.Name + "-shard"
	port := idx.Spec.ServicePort

	pvc := corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "data"},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse(idx.Spec.Storage.Size),
				},
			},
		},
	}
	if idx.Spec.Storage.StorageClassName != "" {
		pvc.Spec.StorageClassName = &idx.Spec.Storage.StorageClassName
	}

	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: idx.Namespace, Labels: labels},
		Spec: appsv1.StatefulSetSpec{
			ServiceName: name,
			Replicas:    &shards,
			Selector:    &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "rpkv",
							Image: idx.Spec.Image,
							Ports: []corev1.ContainerPort{
								{Name: "http", ContainerPort: port},
							},
							Env: []corev1.EnvVar{
								{Name: "RPKV_BROKERS", Value: strings.Join(idx.Spec.Brokers, ",")},
								{Name: "RPKV_TOPICS", Value: idx.Spec.Topic},
								{Name: "RPKV_LISTEN", Value: fmt.Sprintf(":%d", port)},
								{Name: "RPKV_DATA_DIR", Value: "/data"},
								{Name: "RPKV_PARTITION_FROM_ORDINAL", Value: "true"},
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "data", MountPath: "/data"},
							},
							ReadinessProbe: healthzProbe(),
							LivenessProbe:  healthzProbe(),
						},
					},
				},
			},
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{pvc},
		},
	}
}

// newShardService builds the desired headless Service fronting the shard
// StatefulSet's pods, giving each a stable DNS name.
func newShardService(idx *rpkvv1alpha1.RpkvIndex) *corev1.Service {
	labels := workloadLabels(idx, componentShard)

	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: idx.Name + "-shard", Namespace: idx.Namespace, Labels: labels},
		Spec: corev1.ServiceSpec{
			ClusterIP: corev1.ClusterIPNone,
			Selector:  labels,
			Ports: []corev1.ServicePort{
				{Name: "http", Port: idx.Spec.ServicePort},
			},
		},
	}
}

// shardAddrs lists each shard pod's stable DNS name against the headless
// shard Service, one per replica in [0, shards).
func shardAddrs(idx *rpkvv1alpha1.RpkvIndex, shards int32) []string {
	shardName := idx.Name + "-shard"
	port := idx.Spec.ServicePort

	addrs := make([]string, 0, shards)
	for i := int32(0); i < shards; i++ {
		addrs = append(addrs, fmt.Sprintf("%s-%d.%s:%d", shardName, i, shardName, port))
	}
	return addrs
}

// newRouterDeployment builds the desired router Deployment for idx,
// fanning reads out across shards replicas.
func newRouterDeployment(idx *rpkvv1alpha1.RpkvIndex, shards int32) *appsv1.Deployment {
	labels := workloadLabels(idx, componentRouter)
	name := idx.Name + "-router"
	port := idx.Spec.ServicePort
	replicas := idx.Spec.RouterReplicas

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: idx.Namespace, Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "rpkv",
							Image: idx.Spec.Image,
							Ports: []corev1.ContainerPort{
								{Name: "http", ContainerPort: port},
							},
							Env: []corev1.EnvVar{
								{Name: "RPKV_MODE", Value: "router"},
								{Name: "RPKV_SHARDS", Value: strings.Join(shardAddrs(idx, shards), ",")},
								{Name: "RPKV_LISTEN", Value: fmt.Sprintf(":%d", port)},
							},
							ReadinessProbe: healthzProbe(),
							LivenessProbe:  healthzProbe(),
						},
					},
				},
			},
		},
	}
}

// newRouterService builds the desired ClusterIP Service fronting the
// router Deployment's pods.
func newRouterService(idx *rpkvv1alpha1.RpkvIndex) *corev1.Service {
	labels := workloadLabels(idx, componentRouter)

	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: idx.Name + "-router", Namespace: idx.Namespace, Labels: labels},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: labels,
			Ports: []corev1.ServicePort{
				{Name: "http", Port: idx.Spec.ServicePort},
			},
		},
	}
}
