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

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// Manager handles API key lifecycle management
type Manager struct {
	headscaleClient *HeadscaleClient
	secretManager   *SecretManager
	secretName      string
	expiration      time.Duration
	rotationBuffer  time.Duration
	log             logr.Logger
}

// Config holds the configuration for the API key manager
type Config struct {
	SocketPath     string
	Namespace      string
	SecretName     string
	Expiration     string
	RotationBuffer string
}

// NewManager creates a new API key manager
func NewManager(cfg Config, log logr.Logger) (*Manager, error) {
	// Parse durations
	expiration, err := time.ParseDuration(cfg.Expiration)
	if err != nil {
		return nil, fmt.Errorf("failed to parse expiration duration: %w", err)
	}

	rotationBuffer, err := time.ParseDuration(cfg.RotationBuffer)
	if err != nil {
		return nil, fmt.Errorf("failed to parse rotation buffer duration: %w", err)
	}

	// Validate that rotation buffer is less than expiration
	if rotationBuffer >= expiration {
		return nil, fmt.Errorf("rotation buffer (%s) must be less than expiration (%s)",
			cfg.RotationBuffer, cfg.Expiration)
	}

	log.Info("Creating API key manager",
		"expiration", expiration.String(),
		"rotationBuffer", rotationBuffer.String(),
	)

	// Create Headscale client
	headscaleClient, err := NewHeadscaleClient(cfg.SocketPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create Headscale client: %w", err)
	}

	// Create secret manager
	secretManager, err := NewSecretManager(cfg.Namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to create secret manager: %w", err)
	}

	return &Manager{
		headscaleClient: headscaleClient,
		secretManager:   secretManager,
		secretName:      cfg.SecretName,
		expiration:      expiration,
		rotationBuffer:  rotationBuffer,
		log:             log,
	}, nil
}

// Close closes the manager and its resources
func (m *Manager) Close() error {
	return m.headscaleClient.Close()
}

// EnsureAPIKey ensures an API key exists and is valid, creating or rotating it if necessary
func (m *Manager) EnsureAPIKey(ctx context.Context) error {
	// Try to get existing secret
	secret, err := m.secretManager.GetSecret(ctx, m.secretName)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get secret: %w", err)
		}
		// Secret doesn't exist, create new API key
		m.log.Info("API key secret not found, creating new API key")
		return m.createNewAPIKey(ctx)
	}

	// Check if rotation is needed
	expiration, err := GetExpirationFromSecret(secret)
	if err != nil {
		// Invalid secret, create new API key
		m.log.Info("Invalid secret expiration, creating new API key", "error", err)
		return m.createNewAPIKey(ctx)
	}

	if ShouldRotate(expiration, m.rotationBuffer) {
		m.log.Info("API key rotation needed", "expiration", expiration, "rotationBuffer", m.rotationBuffer)
		return m.rotateAPIKey(ctx)
	}

	m.log.V(1).Info("API key is valid, no rotation needed", "expiration", expiration)
	return nil
}

// createNewAPIKey creates a new API key and stores it in a secret
func (m *Manager) createNewAPIKey(ctx context.Context) error {
	m.log.Info("Creating new API key", "expiration", m.expiration.String())

	apiKey, expiration, err := m.headscaleClient.CreateAPIKey(ctx, m.expiration)
	if err != nil {
		return fmt.Errorf("failed to create API key: %w", err)
	}

	m.log.Info("API key created successfully", "expiration", expiration)

	err = m.secretManager.CreateOrUpdateSecret(ctx, m.secretName, apiKey, expiration)
	if err != nil {
		return fmt.Errorf("failed to store API key in secret: %w", err)
	}

	m.log.Info("API key stored in secret", "secretName", m.secretName)
	return nil
}

// rotateAPIKey rotates the API key by creating a new one and expiring the old one
func (m *Manager) rotateAPIKey(ctx context.Context) error {
	m.log.Info("Rotating API key")

	// Get the old API key to expire it later
	secret, err := m.secretManager.GetSecret(ctx, m.secretName)
	var oldAPIKey string
	if err == nil {
		var extractErr error
		oldAPIKey, extractErr = GetAPIKeyFromSecret(secret)
		if extractErr != nil {
			m.log.Info("Could not extract old API key from secret, will skip expiration", "error", extractErr)
		}
	}

	// Create new API key
	apiKey, expiration, err := m.headscaleClient.CreateAPIKey(ctx, m.expiration)
	if err != nil {
		return fmt.Errorf("failed to create new API key: %w", err)
	}

	m.log.Info("New API key created", "expiration", expiration)

	// Update secret with new API key
	err = m.secretManager.CreateOrUpdateSecret(ctx, m.secretName, apiKey, expiration)
	if err != nil {
		return fmt.Errorf("failed to update secret with new API key: %w", err)
	}

	m.log.Info("Secret updated with new API key")

	// Expire old API key if it exists
	if oldAPIKey != "" {
		// Extract prefix from old API key (first 8 characters)
		if len(oldAPIKey) >= 8 {
			prefix := oldAPIKey[:8]
			if err := m.headscaleClient.ExpireAPIKey(ctx, prefix); err != nil {
				m.log.Error(err, "Failed to expire old API key", "prefix", prefix)
				// Don't return error, rotation is complete
			} else {
				m.log.Info("Old API key expired", "prefix", prefix)
			}
		}
	}

	m.log.Info("API key rotation completed successfully")
	return nil
}

// CalculateNextRotation calculates when the next rotation should occur
func (m *Manager) CalculateNextRotation(ctx context.Context) (time.Duration, error) {
	secret, err := m.secretManager.GetSecret(ctx, m.secretName)
	if err != nil {
		return 0, fmt.Errorf("failed to get secret: %w", err)
	}

	expiration, err := GetExpirationFromSecret(secret)
	if err != nil {
		return 0, fmt.Errorf("failed to get expiration from secret: %w", err)
	}

	rotationTime := expiration.Add(-m.rotationBuffer)
	timeUntilRotation := time.Until(rotationTime)

	// If rotation time has passed, return 0 to rotate immediately
	return max(timeUntilRotation, 0), nil
}

// WaitForHeadscale waits for Headscale to be ready
func (m *Manager) WaitForHeadscale(ctx context.Context) error {
	return m.headscaleClient.WaitForReady(ctx, 60, 5*time.Second)
}
