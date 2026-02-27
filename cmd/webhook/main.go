// Package main is the entrypoint for the Nexa Admission Webhook.
// It validates nexa.io/* labels on pods against namespace-scoped rules
// to prevent label spoofing across organizational boundaries.
package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"k8s.io/klog/v2"

	"github.com/nexascheduler/nexa/pkg/webhook"
)

func main() {
	var (
		configPath  string
		certDir     string
		port        int
		metricsPort int
	)

	klog.InitFlags(nil)
	flag.StringVar(&configPath, "config", "/etc/nexa-webhook/config.json", "path to webhook config file")
	flag.StringVar(&certDir, "cert-dir", "/etc/nexa-webhook/certs", "directory containing tls.crt and tls.key")
	flag.IntVar(&port, "port", 8443, "HTTPS port for admission webhook")
	flag.IntVar(&metricsPort, "metrics-port", 8080, "HTTP port for health and metrics")
	flag.Parse()

	cfg, err := webhook.LoadConfigFromFile(configPath)
	if err != nil {
		klog.ErrorS(err, "failed to load webhook config", "path", configPath)
		os.Exit(1)
	}
	klog.InfoS("webhook config loaded", "rules", len(cfg.Rules))

	certFile := filepath.Join(certDir, "tls.crt")
	keyFile := filepath.Join(certDir, "tls.key")
	tlsCert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		klog.ErrorS(err, "failed to load TLS certificate", "certDir", certDir)
		os.Exit(1)
	}

	handler := webhook.NewHandler(cfg)

	tlsServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: handler,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{tlsCert},
			MinVersion:   tls.VersionTLS12,
		},
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
		klog.InfoS("starting webhook server", "port", port)
		if err := tlsServer.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			klog.ErrorS(err, "webhook server failed")
		}
	}()

	<-ctx.Done()
	klog.InfoS("shutting down")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	_ = tlsServer.Shutdown(shutdownCtx)
	_ = metricsServer.Shutdown(shutdownCtx)
}
