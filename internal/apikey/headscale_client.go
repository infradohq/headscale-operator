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

	v1 "github.com/juanfont/headscale/gen/go/headscale/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// HeadscaleClient wraps the Headscale gRPC client
type HeadscaleClient struct {
	conn   *grpc.ClientConn
	client v1.HeadscaleServiceClient
	apiKey string
}

// NewHeadscaleClient creates a new Headscale client connected via Unix socket
func NewHeadscaleClient(socketPath string) (*HeadscaleClient, error) {
	// Connect to Unix socket using gRPC
	conn, err := grpc.NewClient(
		fmt.Sprintf("unix://%s", socketPath),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Headscale socket: %w", err)
	}

	client := v1.NewHeadscaleServiceClient(conn)

	return &HeadscaleClient{
		conn:   conn,
		client: client,
	}, nil
}

// NewHeadscaleClientWithAPIKey creates a new Headscale client connected via gRPC service with API key authentication
func NewHeadscaleClientWithAPIKey(serverAddr string, apiKey string) (*HeadscaleClient, error) {
	// Create a client with API key interceptor
	conn, err := grpc.NewClient(
		serverAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
			if apiKey != "" {
				ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+apiKey)
			}
			return invoker(ctx, method, req, reply, cc, opts...)
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Headscale server: %w", err)
	}

	client := v1.NewHeadscaleServiceClient(conn)

	return &HeadscaleClient{
		conn:   conn,
		client: client,
		apiKey: apiKey,
	}, nil
}

// Close closes the gRPC connection
func (c *HeadscaleClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// CreateAPIKey creates a new API key with the specified expiration
func (c *HeadscaleClient) CreateAPIKey(ctx context.Context, expiration time.Duration) (string, time.Time, error) {
	expirationTime := time.Now().Add(expiration)

	req := &v1.CreateApiKeyRequest{
		Expiration: timestamppb.New(expirationTime),
	}

	resp, err := c.client.CreateApiKey(ctx, req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to create API key: %w", err)
	}

	return resp.GetApiKey(), expirationTime, nil
}

// ExpireAPIKey expires an existing API key
func (c *HeadscaleClient) ExpireAPIKey(ctx context.Context, prefix string) error {
	req := &v1.ExpireApiKeyRequest{
		Prefix: prefix,
	}

	_, err := c.client.ExpireApiKey(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to expire API key: %w", err)
	}

	return nil
}

// ListAPIKeys lists all API keys
func (c *HeadscaleClient) ListAPIKeys(ctx context.Context) ([]*v1.ApiKey, error) {
	req := &v1.ListApiKeysRequest{}

	resp, err := c.client.ListApiKeys(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to list API keys: %w", err)
	}

	return resp.GetApiKeys(), nil
}

// WaitForReady waits for Headscale to be ready by attempting to list API keys
func (c *HeadscaleClient) WaitForReady(ctx context.Context, maxRetries int, retryInterval time.Duration) error {
	for i := 0; i < maxRetries; i++ {
		_, err := c.ListAPIKeys(ctx)
		if err == nil {
			return nil
		}

		if i < maxRetries-1 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(retryInterval):
				continue
			}
		}
	}

	return fmt.Errorf("headscale not ready after %d retries", maxRetries)
}

// CreateUser creates a new user in Headscale
func (c *HeadscaleClient) CreateUser(ctx context.Context, username, displayName, email, pictureURL string) (*v1.User, error) {
	req := &v1.CreateUserRequest{
		Name: username,
	}

	// Only set optional fields if they're provided
	if displayName != "" {
		req.DisplayName = displayName
	}
	if email != "" {
		req.Email = email
	}
	if pictureURL != "" {
		req.PictureUrl = pictureURL
	}

	resp, err := c.client.CreateUser(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to create user: %w", err)
	}

	return resp.GetUser(), nil
}

// DeleteUser deletes a user from Headscale by ID
func (c *HeadscaleClient) DeleteUser(ctx context.Context, userID uint64) error {
	req := &v1.DeleteUserRequest{
		Id: userID,
	}

	_, err := c.client.DeleteUser(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to delete user: %w", err)
	}

	return nil
}

// GetUserByName retrieves a user from Headscale by name
func (c *HeadscaleClient) GetUserByName(ctx context.Context, username string) (*v1.User, error) {
	req := &v1.ListUsersRequest{
		Name: username,
	}

	resp, err := c.client.ListUsers(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to list users: %w", err)
	}

	users := resp.GetUsers()
	if len(users) == 0 {
		return nil, fmt.Errorf("user %s not found", username)
	}

	return users[0], nil
}
