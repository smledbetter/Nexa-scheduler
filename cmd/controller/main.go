// Package main is the entrypoint for the Nexa Node State Controller.
// It watches pod lifecycle events and manages node labels for
// workload tracking and cleanliness state.
package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"
	"time"

	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"

	"github.com/nexascheduler/nexa/pkg/nodestate"
)

func main() {
	var kubeconfig string
	var resyncPeriod time.Duration
	var workers int

	klog.InitFlags(nil)
	flag.StringVar(&kubeconfig, "kubeconfig", "", "path to kubeconfig file (uses in-cluster config if empty)")
	flag.DurationVar(&resyncPeriod, "resync-period", 5*time.Minute, "informer resync period")
	flag.IntVar(&workers, "workers", 2, "number of reconciliation workers")
	flag.Parse()

	config, err := buildConfig(kubeconfig)
	if err != nil {
		klog.ErrorS(err, "failed to build kubeconfig")
		os.Exit(1)
	}

	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		klog.ErrorS(err, "failed to create kubernetes client")
		os.Exit(1)
	}

	factory := informers.NewSharedInformerFactory(client, resyncPeriod)
	controller := nodestate.NewController(client, factory)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	factory.Start(ctx.Done())

	klog.InfoS("node state controller starting", "workers", workers, "resyncPeriod", resyncPeriod)
	if err := controller.Run(ctx, workers); err != nil {
		klog.ErrorS(err, "controller run failed")
		os.Exit(1)
	}
}

func buildConfig(kubeconfig string) (*rest.Config, error) {
	if kubeconfig != "" {
		return clientcmd.BuildConfigFromFlags("", kubeconfig)
	}
	return rest.InClusterConfig()
}
