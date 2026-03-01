// Package main is the entrypoint for the Nexa Evidence Agent.
// It runs on each confidential node as a DaemonSet and serves
// SEV-SNP attestation reports to the MAA adapter.
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

	"k8s.io/klog/v2"

	"github.com/nexascheduler/nexa/pkg/adapter/evidence"
)

func main() {
	var (
		port       int
		reportPath string
	)

	klog.InitFlags(nil)
	flag.IntVar(&port, "port", 9443, "HTTP port for the evidence endpoint")
	flag.StringVar(&reportPath, "report-path", "/dev/sev-guest", "path to the SEV-SNP attestation report device")
	flag.Parse()

	if _, err := os.Stat(reportPath); err != nil {
		klog.InfoS("report path not available (expected on non-CVM nodes)", "path", reportPath, "err", err)
	}

	agent := evidence.NewAgent(reportPath)

	server := &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		Handler:           agent,
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	go func() {
		klog.InfoS("starting evidence agent", "port", port, "reportPath", reportPath)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			klog.ErrorS(err, "server failed")
		}
	}()

	<-ctx.Done()
	klog.InfoS("shutting down")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	_ = server.Shutdown(shutdownCtx)
}
