// Package main is the entrypoint for the Nexa Node State Controller.
// It watches pod lifecycle events and manages node labels for
// workload tracking and cleanliness state. Optionally runs a remote
// attestation controller that verifies TEE node claims.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"

	"github.com/nexascheduler/nexa/pkg/attestation"
	"github.com/nexascheduler/nexa/pkg/nodestate"
)

func main() {
	var kubeconfig string
	var resyncPeriod time.Duration
	var workers int
	var enableAttestation bool
	var attestationURL string
	var attestationInterval time.Duration

	klog.InitFlags(nil)
	flag.StringVar(&kubeconfig, "kubeconfig", "", "path to kubeconfig file (uses in-cluster config if empty)")
	flag.DurationVar(&resyncPeriod, "resync-period", 5*time.Minute, "informer resync period")
	flag.IntVar(&workers, "workers", 2, "number of reconciliation workers")
	flag.BoolVar(&enableAttestation, "enable-attestation", false, "enable remote TEE attestation verification")
	flag.StringVar(&attestationURL, "attestation-url", "", "URL of the remote attestation service (required when --enable-attestation is set)")
	flag.DurationVar(&attestationInterval, "attestation-interval", 5*time.Minute, "interval between attestation verification cycles")
	flag.Parse()

	if enableAttestation && attestationURL == "" {
		fmt.Fprintln(os.Stderr, "--attestation-url is required when --enable-attestation is set")
		os.Exit(1)
	}

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

	// Start attestation controller if enabled.
	if enableAttestation {
		attester := attestation.NewHTTPAttester(attestationURL)
		nodeLister := factory.Core().V1().Nodes().Lister()
		ac := nodestate.NewAttestationController(client, nodeLister, attester, attestationInterval)

		klog.InfoS("attestation controller enabled", "url", attestationURL, "interval", attestationInterval)
		go func() {
			if err := ac.Run(ctx); err != nil {
				klog.ErrorS(err, "attestation controller failed")
			}
		}()
	}

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
