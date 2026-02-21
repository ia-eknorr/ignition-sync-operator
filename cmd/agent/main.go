package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	syncv1alpha1 "github.com/inductiveautomation/ignition-sync-operator/api/v1alpha1"
	"github.com/inductiveautomation/ignition-sync-operator/internal/agent"
)

func main() {
	logf.SetLogger(zap.New(zap.UseDevMode(true)))
	log := logf.Log.WithName("agent")

	log.Info("ignition-sync-agent starting")

	// Load configuration from environment.
	cfg, err := agent.LoadConfig()
	if err != nil {
		log.Error(err, "failed to load config")
		os.Exit(1)
	}

	// Build K8s client.
	k8sClient, err := buildK8sClient()
	if err != nil {
		log.Error(err, "failed to build K8s client")
		os.Exit(1)
	}

	// Create and run the agent.
	a := agent.New(cfg, k8sClient)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := a.Run(logf.IntoContext(ctx, log)); err != nil {
		log.Error(err, "agent exited with error")
		os.Exit(1)
	}

	log.Info("agent shutdown complete")
}

func buildK8sClient() (client.Client, error) {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(syncv1alpha1.AddToScheme(scheme))

	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}

	return client.New(config, client.Options{Scheme: scheme})
}
