// Command rpkv-operator is the process entry point: wiring owner, and
// nothing but wiring.
package main

import (
	"fmt"
	"net/http"
	"os"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	rpkvv1alpha1 "github.com/sonirico/rpkv/operator/api/v1alpha1"
	"github.com/sonirico/rpkv/operator/internal/controller"
)

func main() {
	ctrl.SetLogger(zap.New())
	logger := ctrl.Log.WithName("setup")

	scheme := clientgoscheme.Scheme
	if err := rpkvv1alpha1.AddToScheme(scheme); err != nil {
		logger.Error(err, "add rpkv scheme")
		os.Exit(1)
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:         scheme,
		LeaderElection: false,
	})
	if err != nil {
		logger.Error(err, "build manager")
		os.Exit(1)
	}

	partitionsFor := controller.PartitionSourceFactory(func(brokers []string) (controller.PartitionSource, error) {
		client, err := kgo.NewClient(kgo.SeedBrokers(brokers...))
		if err != nil {
			return nil, fmt.Errorf("rpkv-operator: build kafka client: %w", err)
		}
		return controller.NewKafkaPartitionSource(kadm.NewClient(client)), nil
	})

	reconciler := controller.NewReconciler(
		mgr.GetClient(),
		mgr.GetScheme(),
		partitionsFor,
		controller.NewHTTPHealthSource(&http.Client{}),
	)
	if err := reconciler.SetupWithManager(mgr); err != nil {
		logger.Error(err, "setup reconciler")
		os.Exit(1)
	}

	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		logger.Error(err, "run manager")
		os.Exit(1)
	}
}
