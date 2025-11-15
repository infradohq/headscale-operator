/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-logr/zapr"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/infradohq/headscale-operator/internal/apikey"
)

var (
	socketPath     string
	namespace      string
	secretName     string
	expiration     string
	rotationBuffer string
)

func init() {
	flag.StringVar(&socketPath, "socket-path", "/var/run/headscale/headscale.sock", "Path to Headscale Unix socket")
	flag.StringVar(&namespace, "namespace", "", "Kubernetes namespace (required)")
	flag.StringVar(&secretName, "secret-name", "headscale-api-key", "Name of the Kubernetes secret to store the API key")
	flag.StringVar(&expiration, "expiration", "2160h", "API key expiration duration (e.g., '2160h' for 90 days)")
	flag.StringVar(&rotationBuffer, "rotation-buffer", "1920h", "Time before expiration to rotate the key (e.g., '168h' for 7 days, '240h' for 10 days)")
}

func main() {
	flag.Parse()

	// Setup logger
	zapConfig := zap.NewProductionConfig()
	zapConfig.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	zapLog, err := zapConfig.Build()
	if err != nil {
		panic(err)
	}
	defer func() {
		_ = zapLog.Sync()
	}()
	log := zapr.NewLogger(zapLog)

	if namespace == "" {
		// Try to get namespace from environment variable (set by Kubernetes downward API)
		namespace = os.Getenv("POD_NAMESPACE")
		if namespace == "" {
			log.Error(nil, "namespace is required: use --namespace flag or ensure POD_NAMESPACE environment variable is set by the Kubernetes downward API")
			os.Exit(1)
		}
	}

	log.Info("Starting Headscale API Key Manager",
		"socketPath", socketPath,
		"namespace", namespace,
		"secretName", secretName,
		"expiration", expiration,
		"rotationBuffer", rotationBuffer,
	)

	// Create manager
	mgr, err := apikey.NewManager(apikey.Config{
		SocketPath:     socketPath,
		Namespace:      namespace,
		SecretName:     secretName,
		Expiration:     expiration,
		RotationBuffer: rotationBuffer,
	}, log)
	if err != nil {
		log.Error(err, "Failed to create API key manager")
		os.Exit(1)
	}
	defer func() {
		if err := mgr.Close(); err != nil {
			log.Error(err, "Failed to close manager")
		}
	}()

	// Setup context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Setup signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Wait for Headscale to be ready
	log.Info("Waiting for Headscale to be ready")
	if err := mgr.WaitForHeadscale(ctx); err != nil {
		log.Error(err, "Headscale not ready")
		os.Exit(1)
	}
	log.Info("Headscale is ready")

	// Ensure API key exists and is valid
	log.Info("Ensuring API key exists and is valid")
	if err := mgr.EnsureAPIKey(ctx); err != nil {
		log.Error(err, "Failed to ensure API key")
		os.Exit(1)
	}
	log.Info("API key is ready")

	// Main loop - check for rotation periodically
	log.Info("Starting rotation check loop")
	for {
		// Calculate when to check next
		nextRotation, err := mgr.CalculateNextRotation(ctx)
		if err != nil {
			log.Error(err, "Failed to calculate next rotation, will retry in 1 hour")
			nextRotation = 1 * time.Hour
		}

		// Sleep until it's time to check
		log.Info("Sleeping until next check", "duration", nextRotation.String())

		select {
		case <-ctx.Done():
			log.Info("Context cancelled, exiting")
			return
		case sig := <-sigChan:
			log.Info("Received signal, exiting", "signal", sig)
			cancel()
			return
		case <-time.After(nextRotation):
			log.Info("Checking if API key rotation is needed")
			if err := mgr.EnsureAPIKey(ctx); err != nil {
				log.Error(err, "Failed to ensure API key")
				// Don't exit, just log and continue
			}
		}
	}
}
