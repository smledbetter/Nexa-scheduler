// Package main is the entrypoint for the Nexa Azure MAA Attestation Adapter.
// It bridges Nexa's attestation controller to Microsoft Azure Attestation (MAA)
// by fetching platform evidence from per-node agents and forwarding it to MAA.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"

	"github.com/nexascheduler/nexa/pkg/adapter/maa"
)

func main() {
	var (
		maaEndpoint  string
		evidencePort int
		port         int
		metricsPort  int
		kubeconfig   string
	)

	klog.InitFlags(nil)
	flag.StringVar(&maaEndpoint, "maa-endpoint", "", "Azure MAA endpoint URL (e.g., https://sharedeus.eus.attest.azure.net)")
	flag.IntVar(&evidencePort, "evidence-port", 9443, "port where evidence agents listen on each node")
	flag.IntVar(&port, "port", 8080, "HTTP port for the verify endpoint")
	flag.IntVar(&metricsPort, "metrics-port", 9090, "HTTP port for health and metrics")
	flag.StringVar(&kubeconfig, "kubeconfig", "", "path to kubeconfig (uses in-cluster config if empty)")
	flag.Parse()

	if maaEndpoint == "" {
		fmt.Fprintln(os.Stderr, "--maa-endpoint is required")
		os.Exit(1)
	}

	config, err := buildConfig(kubeconfig)
	if err != nil {
		klog.ErrorS(err, "failed to build kube config")
		os.Exit(1)
	}

	kubeClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		klog.ErrorS(err, "failed to create kube client")
		os.Exit(1)
	}

	handler := maa.NewHandler(maaEndpoint, evidencePort, kubeClient)

	apiServer := &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	metricsServer := &http.Server{
		Addr:              fmt.Sprintf(":%d", metricsPort),
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	go func() {
		klog.InfoS("starting metrics server", "port", metricsPort)
		if err := metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			klog.ErrorS(err, "metrics server failed")
		}
	}()

	go func() {
		klog.InfoS("starting MAA adapter", "port", port, "maaEndpoint", maaEndpoint, "evidencePort", evidencePort)
		if err := apiServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			klog.ErrorS(err, "api server failed")
		}
	}()

	<-ctx.Done()
	klog.InfoS("shutting down")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	_ = apiServer.Shutdown(shutdownCtx)
	_ = metricsServer.Shutdown(shutdownCtx)
}

func buildConfig(kubeconfig string) (*rest.Config, error) {
	if kubeconfig != "" {
		return clientcmd.BuildConfigFromFlags("", kubeconfig)
	}
	return rest.InClusterConfig()
}
