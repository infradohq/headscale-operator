# Headscale API Key Manager

The API Key Manager is a sidecar container that runs alongside Headscale to automatically manage API keys for the operator.

## Overview

The API Key Manager:
- Automatically creates an API key when Headscale starts
- Stores the API key in a Kubernetes Secret
- Automatically rotates the API key before it expires
- Communicates with Headscale via Unix socket

## Architecture

```
┌─────────────────────────────────────────┐
│           Headscale Pod                 │
│                                         │
│  ┌──────────────┐    ┌───────────────┐  │
│  │              │    │               │  │
│  │  Headscale   │◄──►│  API Key      │  │
│  │  Container   │    │  Manager      │  │
│  │              │    │  Sidecar      │  │
│  └──────────────┘    └───────────────┘  │
│         │                    │          │
│         │ Unix Socket        │          │
│         └────────────────────┘          │
│                              │          │
└──────────────────────────────┼──────────┘
                               │
                               ▼
                    ┌──────────────────┐
                    │  Kubernetes      │
                    │  Secret          │
                    │                  │
                    │  api-key:        │
                    │  expiration:     │
                    │  created-at:     │
                    └──────────────────┘
```

## Configuration

The API Key Manager is configured via the `apiKey` section in the Headscale CRD:

```yaml
apiVersion: headscale.infrado.cloud/v1beta1
kind: Headscale
metadata:
  name: headscale-sample
spec:
  # ... other configuration ...
  
  apiKey:
    # Enable/disable automatic API key management (default: true)
    autoManage: true
    
    # Name of the Kubernetes secret to store the API key (default: "headscale-api-key")
    secretName: headscale-api-key
    
    # API key expiration duration in Go duration format (default: "2160h")
    # Examples: "720h" (30 days), "2160h" (90 days), "8760h" (365 days)
    # Supported units: h (hours), m (minutes), s (seconds)
    # Note: Use hours for days (24h = 1 day)
    expiration: "2160h"
    
    # Time before expiration to rotate the key (default: "1920h" = 80 days)
    # Must be less than expiration
    # Examples: "168h" (7 days), "240h" (10 days), "480h" (20 days)
    rotationBuffer: "240h"
```

## How It Works

### Initial Setup

1. When a Headscale pod starts, the API Key Manager sidecar waits for Headscale to be ready
2. Once ready, it checks if an API key secret exists
3. If not, it creates a new API key via the Headscale gRPC API
4. It stores the API key in a Kubernetes Secret

### Key Rotation

1. The sidecar periodically checks (every hour) if rotation is needed
2. Rotation is triggered when: `current_time >= (expiration_time - rotation_buffer)`
3. When rotating:
   - Creates a new API key
   - Updates the Kubernetes Secret with the new key
   - Expires the old API key in Headscale

### Example Timeline

With `expiration: "2160h"` (90 days) and `rotationBuffer: "240h"` (10 days):

```
Day 0:   API key created (expires in 90 days / 2160 hours)
Day 10:  Rotation check - no rotation needed (80 days remaining)
Day 20:  Rotation check - no rotation needed (70 days remaining)
...
Day 80:  Rotation check - no rotation needed (10 days / 240h remaining)
Day 81:  Rotation check - ROTATION TRIGGERED (9 days < 10 day buffer)
         New key created, old key expired
```

## Secret Format

The created secret contains:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: headscale-api-key
  labels:
    app.kubernetes.io/managed-by: headscale-operator
    app.kubernetes.io/component: api-key
  annotations:
    api-key.headscale.infrado.cloud/expiration: <RFC3339-timestamp>
    api-key.headscale.infrado.cloud/created-at: <RFC3339-timestamp>
type: Opaque
data:
  api-key: <base64-encoded-api-key>
```

## Building

The API Key Manager can be built separately:

```bash
# Build the binary
go build -o bin/apikey-manager cmd/apikey-manager/main.go

# Build the Docker image
docker build -f Dockerfile.apikey-manager -t headscale-operator/apikey-manager:latest .
```

## Manual Usage

For testing or manual deployment:

```bash
./apikey-manager \
  --socket-path=/var/run/headscale/headscale.sock \
  --namespace=default \
  --secret-name=headscale-api-key \
  --expiration=2160h \
  --rotation-buffer=240h
```

## Troubleshooting

### API Key Manager fails to start

**Check Headscale readiness:**
```bash
kubectl logs <pod-name> -c apikey-manager
```

Look for messages like "Waiting for Headscale to be ready..."

### API Key rotation not happening

**Check rotation logic:**
```bash
# View the expiration timestamp (stored in annotations)
kubectl get secret headscale-api-key -o jsonpath='{.metadata.annotations.api-key\.headscale\.infrado\.cloud/expiration}'

# Check sidecar logs
kubectl logs <pod-name> -c apikey-manager
```

### Permission issues

Ensure the ServiceAccount has the necessary RBAC permissions:

```bash
kubectl auth can-i create secrets --as=system:serviceaccount:default:headscale
kubectl auth can-i update secrets --as=system:serviceaccount:default:headscale
```

## Development

### Adding to an existing Headscale deployment

The sidecar is automatically injected when `spec.apiKey.autoManage` is `true` (the default).

To disable automatic API key management:

```yaml
spec:
  apiKey:
    autoManage: false
```

### Testing locally

You can test the API key manager against a local Headscale instance:

1. Start Headscale with Unix socket enabled
2. Run the API key manager:
   ```bash
   go run cmd/apikey-manager/main.go \
     --socket-path=/var/run/headscale/headscale.sock \
     --namespace=test \
     --secret-name=test-api-key \
     --expiration=1h \
     --rotation-buffer=30m
   ```

## Security Considerations

- The API key manager requires access to create/update Secrets in the namespace
- The Unix socket is shared between containers via an EmptyDir volume (ephemeral)
- API keys are stored in Kubernetes Secrets (consider using encrypted Secrets at rest)
- Old API keys are immediately expired when rotation occurs
- The sidecar uses a minimal distroless image for reduced attack surface
