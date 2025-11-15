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

package controller

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	headscalev1beta1 "github.com/infradohq/headscale-operator/api/v1beta1"
)

// extractPort extracts the port number from an address string like "127.0.0.1:8080" or ":8080"
func extractPort(addr string, defaultPort int32) int32 {
	if addr == "" {
		return defaultPort
	}

	// Split by colon to get the port part
	parts := strings.Split(addr, ":")
	if len(parts) < 2 {
		return defaultPort
	}

	portStr := parts[len(parts)-1]
	port, err := strconv.ParseInt(portStr, 10, 32)
	if err != nil {
		return defaultPort
	}

	return int32(port)
}

// getAPIKey retrieves the API key from the secret created by the apikey-manager sidecar.
// This function is shared across all controllers that need to interact with the Headscale API.
func getAPIKey(ctx context.Context, k8sClient client.Client, headscale *headscalev1beta1.Headscale) (string, error) {
	// Get the secret name from the Headscale spec
	secretName := headscale.Spec.APIKey.SecretName
	if secretName == "" {
		secretName = defaultAPIKeySecretName
	}

	// Get the secret
	secret := &corev1.Secret{}
	err := k8sClient.Get(ctx, types.NamespacedName{
		Name:      secretName,
		Namespace: headscale.Namespace,
	}, secret)
	if err != nil {
		return "", fmt.Errorf("failed to get API key secret: %w", err)
	}

	// Get the API key from the secret data
	apiKeyBytes, ok := secret.Data["api-key"]
	if !ok {
		return "", fmt.Errorf("api-key not found in secret %s", secretName)
	}

	return string(apiKeyBytes), nil
}

// getGRPCServiceAddress returns the gRPC service address for the Headscale instance.
// This function is shared across all controllers that need to connect to the Headscale gRPC service.
// It returns the service address in the format: <service-name>.<namespace>.svc:<port>
func getGRPCServiceAddress(headscale *headscalev1beta1.Headscale) string {
	// Extract the gRPC port from the configuration
	grpcPort := extractPort(headscale.Spec.Config.GRPCListenAddr, 50443)

	// Return the service address and let Kubernetes DNS search domain handle the rest
	return fmt.Sprintf("%s.%s.svc:%d", serviceName, headscale.Namespace, grpcPort)
}
