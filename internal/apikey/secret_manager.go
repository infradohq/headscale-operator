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

package apikey

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	// SecretKeyAPIKey is the key name for the API key in the secret data
	SecretKeyAPIKey = "api-key"
	// AnnotationExpiration is the annotation key for the expiration time
	AnnotationExpiration = "api-key.headscale.infrado.cloud/expiration"
	// AnnotationCreatedAt is the annotation key for the creation time
	AnnotationCreatedAt = "api-key.headscale.infrado.cloud/created-at"
)

// SecretManager manages Kubernetes secrets for API keys
type SecretManager struct {
	clientset *kubernetes.Clientset
	namespace string
}

// NewSecretManager creates a new SecretManager
func NewSecretManager(namespace string) (*SecretManager, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get in-cluster config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes clientset: %w", err)
	}

	return &SecretManager{
		clientset: clientset,
		namespace: namespace,
	}, nil
}

// CreateOrUpdateSecret creates or updates a secret with the API key
func (sm *SecretManager) CreateOrUpdateSecret(ctx context.Context, secretName, apiKey string, expiration time.Time) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: sm.namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "headscale-operator",
				"app.kubernetes.io/component":  "api-key",
			},
			Annotations: map[string]string{
				AnnotationExpiration: expiration.Format(time.RFC3339),
				AnnotationCreatedAt:  time.Now().Format(time.RFC3339),
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			SecretKeyAPIKey: []byte(apiKey),
		},
	}

	// Try to get existing secret
	existingSecret, err := sm.GetSecret(ctx, secretName)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// Create new secret
			_, err = sm.clientset.CoreV1().Secrets(sm.namespace).Create(ctx, secret, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("failed to create secret: %w", err)
			}
			return nil
		}
		return fmt.Errorf("failed to get secret: %w", err)
	}

	// Update existing secret
	existingSecret.Data = secret.Data
	existingSecret.Labels = secret.Labels

	// Update annotations, but preserve the original creation timestamp
	if existingSecret.Annotations == nil {
		existingSecret.Annotations = make(map[string]string)
	}
	existingSecret.Annotations[AnnotationExpiration] = secret.Annotations[AnnotationExpiration]
	// Only set creation timestamp if it doesn't exist
	if _, exists := existingSecret.Annotations[AnnotationCreatedAt]; !exists {
		existingSecret.Annotations[AnnotationCreatedAt] = secret.Annotations[AnnotationCreatedAt]
	}
	_, err = sm.clientset.CoreV1().Secrets(sm.namespace).Update(ctx, existingSecret, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update secret: %w", err)
	}

	return nil
}

// GetSecret retrieves a secret
func (sm *SecretManager) GetSecret(ctx context.Context, secretName string) (*corev1.Secret, error) {
	secret, err := sm.clientset.CoreV1().Secrets(sm.namespace).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get secret %s: %w", secretName, err)
	}
	return secret, nil
}

// GetAPIKeyFromSecret extracts the API key from a secret
func GetAPIKeyFromSecret(secret *corev1.Secret) (string, error) {
	apiKey, ok := secret.Data[SecretKeyAPIKey]
	if !ok {
		return "", fmt.Errorf("API key not found in secret")
	}
	return string(apiKey), nil
}

// GetExpirationFromSecret extracts the expiration time from a secret
func GetExpirationFromSecret(secret *corev1.Secret) (time.Time, error) {
	expirationStr, ok := secret.Annotations[AnnotationExpiration]
	if !ok {
		return time.Time{}, fmt.Errorf("expiration not found in secret annotations")
	}

	expiration, err := time.Parse(time.RFC3339, expirationStr)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to parse expiration: %w", err)
	}

	return expiration, nil
}

// ShouldRotate checks if the API key should be rotated based on the rotation buffer
func ShouldRotate(expiration time.Time, rotationBuffer time.Duration) bool {
	rotationTime := expiration.Add(-rotationBuffer)
	return time.Now().After(rotationTime)
}
