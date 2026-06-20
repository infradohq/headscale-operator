# Headscale Operator

A Kubernetes operator for managing [Headscale](https://github.com/juanfont/headscale) - an open source, self-hosted implementation of the Tailscale control server.

## Overview

The Headscale Operator simplifies the deployment and management of Headscale instances on Kubernetes. It provides a declarative, GitOps-friendly way to configure and deploy Headscale with all its configuration options through Kubernetes Custom Resources.

## Features

- **Declarative Configuration**: Define your entire Headscale setup as a Kubernetes Custom Resource
- **Automatic Deployment**: Manages StatefulSets, Services, ConfigMaps, and PersistentVolumes
- **API Key Management**: Automatic API key creation and rotation with configurable expiration
- **ACL Policy Management**: Declarative auto-approval of subnet routes and exit nodes via the `HeadscaleAutoApprover` CRD
- **Full Config Support**: `spec.config` is a free-form passthrough to Headscale's `config.yaml` — every key Headscale supports works (database, DERP, DNS/MagicDNS, OIDC, TLS/Let's Encrypt, policy, …), and new/renamed keys in future Headscale releases need no operator change. Headscale validates the config itself at startup; the result is reflected on the `Headscale` status.
- **Observability**: Built-in metrics endpoint for monitoring
- **Production Ready**: Supports high availability with persistent storage

## Table of Contents

- [Headscale Operator](#headscale-operator)
  - [Overview](#overview)
  - [Features](#features)
  - [Table of Contents](#table-of-contents)
  - [Getting Started](#getting-started)
    - [Prerequisites](#prerequisites)
    - [Installation](#installation)
      - [Using Helm (Recommended)](#using-helm-recommended)
    - [Quick Start](#quick-start)
  - [Usage](#usage)
    - [Managing Users](#managing-users)
      - [Creating a User](#creating-a-user)
      - [User Properties](#user-properties)
      - [Viewing Users](#viewing-users)
    - [Managing PreAuth Keys](#managing-preauth-keys)
      - [Creating a PreAuth Key](#creating-a-preauth-key)
      - [PreAuth Key Properties](#preauth-key-properties)
      - [Retrieving PreAuth Keys](#retrieving-preauth-keys)
      - [Using PreAuth Keys](#using-preauth-keys)
      - [PreAuth Key Examples](#preauth-key-examples)
    - [Managing Auto-Approve Routes](#managing-auto-approve-routes)
      - [Prerequisites](#prerequisites-1)
      - [Creating an Auto-Approver](#creating-an-auto-approver)
      - [Auto-Approver Properties](#auto-approver-properties)
      - [Viewing Auto-Approvers](#viewing-auto-approvers)
      - [Combining With PreAuth Keys](#combining-with-preauth-keys)
      - [Auto-Approver Examples](#auto-approver-examples)
    - [API Key Management](#api-key-management)
    - [Uninstallation](#uninstallation)
  - [Development](#development)
    - [Building from Source](#building-from-source)
    - [Running Locally](#running-locally)
    - [Deploying to Cluster](#deploying-to-cluster)
    - [Running Tests](#running-tests)
  - [Contributing](#contributing)
  - [Acknowledgments](#acknowledgments)

## Getting Started

### Prerequisites

- Go 1.25.0+
- Docker 17.03+
- kubectl 1.11.3+
- Access to a Kubernetes 1.11.3+ cluster

### Installation

#### Using Helm (Recommended)

```sh
helm install headscale-operator oci://ghcr.io/infradohq/headscale-operator/charts/headscale-operator:$LATEST_VERSION
```

### Quick Start

1. Create a namespace for Headscale:

```sh
kubectl create namespace headscale
```

1. Create a Headscale instance:

```yaml
apiVersion: headscale.infrado.cloud/v1beta1
kind: Headscale
metadata:
  name: headscale-sample
  namespace: headscale
spec:
  version: "v0.28.0"
  replicas: 1
  config:
    server_url: http://vpn.headscale.local
    grpc_allow_insecure: true
    # Persistent paths must live under /var/lib/headscale (the data volume mount).
    noise:
      private_key_path: /var/lib/headscale/noise_private.key
    prefixes:
      v4: 100.64.0.0/10
      v6: fd7a:115c:a1e0::/48
    derp:
      server:
        enabled: false
      urls:
        - https://controlplane.tailscale.com/derpmap/default
    disable_check_updates: false
    database:
      type: sqlite
      sqlite:
        path: /var/lib/headscale/db.sqlite
    dns:
      magic_dns: false
      override_local_dns: false
  # Automatic API key management (optional)
  api_key:
    auto_manage: true  # Automatically create and rotate API keys
    manager_image: headscale-api-key
    expiration: "2160h"      # API key expires in 90 days (2160 hours)
    rotation_buffer: "240h"   # Rotate 10 days (240 hours) before expiration
```

> **About `spec.config`:** this maps 1:1 to Headscale's `config.yaml` and is passed
> through verbatim — the operator does not validate individual keys, so any option
> supported by the Headscale version you deploy is accepted (and removed/renamed
> keys won't break the CRD). Headscale validates the config when it starts; a bad
> config surfaces as a crashing pod whose error is reported on the `Headscale`
> resource's `Ready` condition (and as an event). The example above is a complete,
> minimal config — you own it, so include everything Headscale requires (e.g.
> `noise.private_key_path`, at least one `prefixes` range, a `database.sqlite.path`
> when using SQLite, a DERP map via `derp.urls` or an embedded `derp.server`, and a
> `dns` block — `dns.override_local_dns` defaults to `true`, which then requires
> `dns.nameservers.global`). Put persistent paths under
> `/var/lib/headscale` (the data volume mount). The only keys the operator fills in
> for you are the ones it has to wire to Kubernetes itself, when omitted:
> `listen_addr` (`0.0.0.0:8080`), `metrics_listen_addr` (`0.0.0.0:9090`),
> `grpc_listen_addr` (`0.0.0.0:50443`), and `unix_socket`
> (`/var/run/headscale/headscale.sock`); it also requires `server_url`
> (`ConfigValid` condition).

Apply the configuration:

```sh
kubectl apply -f config/samples/headscale_v1beta1_headscale.yaml
```

Or use kustomize:

```sh
kubectl apply -k config/samples/
```

## Usage

The operator will automatically create and manage the following resources:

- A StatefulSet running Headscale
- A ConfigMap with the Headscale configuration
- Services for HTTP, gRPC, and metrics endpoints
- PersistentVolumeClaims for data storage
- API key management sidecar (if enabled)
- Kubernetes Secret with the API key (if auto-managed)

### Managing Users

The operator provides the `HeadscaleUser` custom resource to manage users in your Headscale instance.

#### Creating a User

```yaml
apiVersion: headscale.infrado.cloud/v1beta1
kind: HeadscaleUser
metadata:
  name: alice
  namespace: headscale
spec:
  # Reference to the Headscale instance
  headscaleRef: headscale-sample
  
  # Username (immutable after creation)
  username: alice
  
  # Optional: Display name for the user
  displayName: Alice Smith
  
  # Optional: Email address
  email: alice@example.com
  
  # Optional: Profile picture URL
  pictureURL: https://example.com/alice.jpg
```

Apply the user:

```sh
kubectl apply -f headscaleuser.yaml
```

#### User Properties

- **username**: Must be unique and follow DNS label rules (lowercase alphanumeric with hyphens). This field is **immutable** after creation.
- **displayName**: Human-readable name (max 255 characters). Immutable after creation.
- **email**: Valid email address (max 320 characters). Immutable after creation.
- **pictureURL**: HTTP(S) URL to profile picture (max 2048 characters). Immutable after creation.

#### Viewing Users

```sh
# List all users
kubectl get headscaleuser -n headscale

# Get user details
kubectl get headscaleuser alice -n headscale -o yaml

# View user status including Headscale UserID
kubectl get headscaleuser alice -n headscale -o jsonpath='{.status.userId}'
```

### Managing PreAuth Keys

The operator provides the `HeadscalePreAuthKey` custom resource to manage pre-authentication keys for registering nodes.

#### Creating a PreAuth Key

```yaml
apiVersion: headscale.infrado.cloud/v1beta1
kind: HeadscalePreAuthKey
metadata:
  name: dev-key
  namespace: headscale
spec:
  # Reference to the Headscale instance
  headscaleRef: headscale-sample
  
  # Reference to a HeadscaleUser resource
  headscaleUserRef: alice
  
  # Alternatively, specify user ID directly:
  # userId: 1
  
  # Key expires after 24 hours
  expiration: "24h"
  
  # Can be used only once (set to true for multiple uses)
  reusable: false
  
  # Creates ephemeral nodes (automatically removed when disconnected)
  ephemeral: false
  
  # Automatically assign tags to nodes using this key
  tags:
    - "tag:dev"
    - "tag:laptop"
  
  # Optional: Secret name (defaults to resource name)
  secretName: alice-dev-key
```

Apply the preauth key:

```sh
kubectl apply -f headscalepreauthkey.yaml
```

#### PreAuth Key Properties

- **headscaleRef**: Name of the Headscale instance (required)
- **headscaleUserRef**: Name of the HeadscaleUser resource (use this OR userId)
- **userId**: Numeric user ID from Headscale (use this OR headscaleUserRef)
- **expiration**: Duration string (e.g., "30m", "24h", "1h30m"). Default: "1h"
- **reusable**: Whether key can be used multiple times. Default: false
- **ephemeral**: Whether nodes should be ephemeral. Default: false
- **tags**: List of tags to assign (format: "tag:name")
- **secretName**: Name of the Kubernetes secret to store the key. Defaults to the resource name.

#### Retrieving PreAuth Keys

The generated preauth key is stored in a Kubernetes Secret:

```sh
# Get the preauth key
kubectl get secret alice-dev-key -n headscale -o jsonpath='{.data.key}' | base64 -d

# View all preauth keys
kubectl get headscalepreauthkey -n headscale

# View details
kubectl get headscalepreauthkey dev-key -n headscale -o yaml
```

#### Using PreAuth Keys

Use the retrieved key to register a new node to your Headscale network:

```sh
# Get the key
KEY=$(kubectl get secret alice-dev-key -n headscale -o jsonpath='{.data.key}' | base64 -d)

# Register a node using Tailscale client
tailscale up --login-server=https://headscale.example.com --authkey=$KEY
```

#### PreAuth Key Examples

**One-time use key for a single device:**

```yaml
apiVersion: headscale.infrado.cloud/v1beta1
kind: HeadscalePreAuthKey
metadata:
  name: laptop-key
spec:
  headscaleRef: headscale-sample
  headscaleUserRef: alice
  expiration: "1h"
  reusable: false
```

**Reusable key for multiple CI/CD runners:**

```yaml
apiVersion: headscale.infrado.cloud/v1beta1
kind: HeadscalePreAuthKey
metadata:
  name: ci-runner-key
spec:
  headscaleRef: headscale-sample
  headscaleUserRef: ci-user
  expiration: "720h"  # 30 days
  reusable: true
  ephemeral: true     # Auto-cleanup disconnected runners
  tags:
    - "tag:ci"
    - "tag:ephemeral"
```

**Key for temporary test environments:**

```yaml
apiVersion: headscale.infrado.cloud/v1beta1
kind: HeadscalePreAuthKey
metadata:
  name: test-env-key
spec:
  headscaleRef: headscale-sample
  userId: 5
  expiration: "2h"
  ephemeral: true
  tags:
    - "tag:test"
```

### Managing Auto-Approve Routes

The operator provides the `HeadscaleAutoApprover` custom resource to declaratively manage [auto-approved subnet routes and exit nodes](https://headscale.net/stable/ref/routes/#automatically-approve-routes-of-a-subnet-router) in your Headscale instance. Each `HeadscaleAutoApprover` contributes entries to the parent Headscale's policy document; the operator merges all auto-approvers targeting a Headscale and pushes the result via the gRPC `SetPolicy` API.

#### Prerequisites

Auto-approval lives in the Headscale policy document, which the operator can only write when Headscale is configured with `policy.mode: database`. The parent `Headscale` resource must also declare the tag owners that the auto-approver references. Both fields are set on the `Headscale` spec:

```yaml
apiVersion: headscale.infrado.cloud/v1beta1
kind: Headscale
metadata:
  name: headscale-sample
  namespace: headscale
spec:
  # ...
  config:
    policy:
      mode: database          # required for SetPolicy
  acl_policy:
    tag_owners:
      "tag:router": ["admin@example.com"]
      "tag:exit":   ["admin@example.com"]
    inline: |                 # optional base policy for acls/groups/hosts/ssh
      {
        "acls": [{"action": "accept", "src": ["*"], "dst": ["*:*"]}]
      }
```

`acl_policy.inline` accepts JSON or HuJSON (comments and trailing commas are fine). The operator parses it, merges in `tag_owners` and any `HeadscaleAutoApprover` entries, then pushes the result via `SetPolicy`. The CR is the source of truth — `headscale policy get` shows the rendered output as strict JSON.

#### Creating an Auto-Approver

```yaml
apiVersion: headscale.infrado.cloud/v1beta1
kind: HeadscaleAutoApprover
metadata:
  name: k8s-network
  namespace: headscale
spec:
  # Reference to the Headscale instance (same namespace)
  headscaleRef: headscale-sample

  # Routes a node carrying any of the listed tags will have
  # auto-approved when announced via --advertise-routes.
  routes:
    - cidr: 10.10.0.0/16
      tags: ["tag:router"]

  # Tags whose nodes will be auto-approved as exit nodes.
  exitNodeTags: ["tag:exit"]
```

Apply:

```sh
kubectl apply -f headscaleautoapprover.yaml
```

#### Auto-Approver Properties

- **headscaleRef**: Name of the Headscale instance in the same namespace. Required.
- **routes**: List of `{cidr, tags}` pairs. A node registered with one of the listed tags has the matching CIDR auto-approved when it advertises that route. Each tag must be declared in the parent's `acl_policy.tag_owners`.
- **exitNodeTags**: List of tags whose nodes are auto-approved as exit nodes when they advertise themselves as such.

At least one of `routes` or `exitNodeTags` must be specified. Multiple `HeadscaleAutoApprover` resources may target the same Headscale; the operator merges them deterministically into a single `autoApprovers` block.

#### Viewing Auto-Approvers

```sh
# List all auto-approvers in a namespace
kubectl get headscaleautoapprover -n headscale

# Inspect the Ready condition (status=True / reason=PolicyApplied means push succeeded)
kubectl get headscaleautoapprover k8s-network -n headscale \
  -o jsonpath='{.status.conditions[?(@.type=="Ready")]}'

# Inspect the live merged policy stored in Headscale
kubectl exec -n headscale -c headscale <headscale-pod> -- headscale policy get
```

The `Ready` condition reasons are:

- `PolicyApplied` — the merged policy was successfully pushed via gRPC.
- `HeadscaleNotFound` — the referenced Headscale doesn't exist in the same namespace.
- `PolicyModeUnsupported` — the parent Headscale isn't configured with `policy.mode: database`.
- `PolicyPushFailed` — the gRPC `SetPolicy` call returned an error (see `.status.conditions[].message` for details).

#### Combining With PreAuth Keys

An auto-approver only takes effect when a node is actually carrying the matching tag. The most common pattern is to issue a tagged preauth key for the subnet router:

```yaml
apiVersion: headscale.infrado.cloud/v1beta1
kind: HeadscalePreAuthKey
metadata:
  name: subnet-router-key
  namespace: headscale
spec:
  headscaleRef: headscale-sample
  headscaleUserRef: alice
  reusable: true
  tags: ["tag:router"]
```

A node registered with this key, advertising e.g. `--advertise-routes=10.10.0.0/16`, will have its routes auto-approved.

#### Auto-Approver Examples

**Auto-approve a Kubernetes pod CIDR via a subnet router:**

```yaml
apiVersion: headscale.infrado.cloud/v1beta1
kind: HeadscaleAutoApprover
metadata:
  name: pod-network
spec:
  headscaleRef: headscale-sample
  routes:
    - cidr: 10.244.0.0/16
      tags: ["tag:router"]
```

**Auto-approve a tagged exit node:**

```yaml
apiVersion: headscale.infrado.cloud/v1beta1
kind: HeadscaleAutoApprover
metadata:
  name: home-exit-node
spec:
  headscaleRef: headscale-sample
  exitNodeTags: ["tag:exit"]
```

**Per-team segmentation — each team owns its own CIDR:**

```yaml
---
apiVersion: headscale.infrado.cloud/v1beta1
kind: HeadscaleAutoApprover
metadata:
  name: team-prod-routes
  namespace: headscale
spec:
  headscaleRef: headscale-sample
  routes:
    - cidr: 10.10.0.0/16
      tags: ["tag:router"]
---
apiVersion: headscale.infrado.cloud/v1beta1
kind: HeadscaleAutoApprover
metadata:
  name: team-staging-routes
  namespace: headscale
spec:
  headscaleRef: headscale-sample
  routes:
    - cidr: 10.20.0.0/16
      tags: ["tag:router"]
```

The operator merges both into a single `autoApprovers.routes` map and pushes one consolidated policy.

### API Key Management

When API key auto-management is enabled, the sidecar creates a Kubernetes Secret containing the API key:

```sh
# Get the API key
kubectl get secret headscale-api-key -n headscale -o jsonpath='{.data.api-key}' | base64 -d

# View full secret details
kubectl get secret headscale-api-key -n headscale -o yaml
```

The secret contains:

- `api-key`: The actual API key for authenticating with Headscale
- `expiration`: When the API key will expire (RFC3339 format)
- `created-at`: When the API key was created (RFC3339 format)

For more details on API key management, see [cmd/apikey-manager/README.md](cmd/apikey-manager/README.md).

### Uninstallation

Delete the Headscale instance:

```sh
kubectl delete -k config/samples/
```

To completely remove the operator:

```sh
helm uninstall headscale-operator
```

## Development

### Building from Source

```sh
# Build the operator binary
make build

# Run tests
make test

# Build Docker image
make docker-build IMG=<registry>/headscale-operator:tag
```

### Running Locally

```sh
# Install CRDs
make install

# Run the operator locally (outside the cluster)
make run

# In another terminal, create a sample Headscale instance
kubectl apply -f config/samples/headscale_v1beta1_headscale.yaml
```

### Deploying to Cluster

```sh
# Build and push image
make docker-build docker-push IMG=<registry>/headscale-operator:tag

# Deploy to cluster
make deploy IMG=<registry>/headscale-operator:tag
```

### Running Tests

```sh
# Run unit tests
make test

# Run end-to-end tests
make test-e2e
```

## Contributing

We welcome contributions! Here's how you can help:

1. **Fork the repository** and create your branch from `main`
2. **Make your changes** and add tests if applicable
3. **Ensure tests pass** by running `make test`
4. **Format your code** with `make fmt` and `make vet`
5. **Commit your changes** using conventional commits
6. **Open a pull request** with a clear description of your changes

## Acknowledgments

- [Headscale](https://github.com/juanfont/headscale) - The awesome project this operator manages
- [Kubebuilder](https://book.kubebuilder.io/) - The framework used to build this operator
- All our [contributors](https://github.com/infradohq/headscale-operator/graphs/contributors)
