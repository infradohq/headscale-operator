# headscale-operator

A Kubernetes operator for managing [Headscale](https://github.com/juanfont/headscale) - an open source, self-hosted implementation of the Tailscale control server.

## Description

The Headscale Operator simplifies the deployment and management of Headscale instances on Kubernetes. It provides a declarative way to configure and deploy Headscale with all its configuration options through a Kubernetes Custom Resource.

### Features

- **Declarative Configuration**: Define your entire Headscale configuration as a Kubernetes CR
- **Automatic Deployment**: Manages StatefulSet, Services, ConfigMaps, and PersistentVolumes
- **API Key Management**: Automatic API key creation and rotation for seamless integration
- **Full Config Support**: Supports all Headscale configuration options including:
  - Database configuration (SQLite/PostgreSQL)
  - DERP server configuration
  - DNS and MagicDNS settings
  - OIDC authentication
  - TLS/Let's Encrypt integration
  - Policy configuration
- **High Availability**: Supports multiple replicas with persistent storage
- **Metrics**: Exposes metrics endpoint for monitoring

## Getting Started

### Prerequisites
- go version v1.25.0+
- docker version 17.03+.
- kubectl version v1.11.3+.
- Access to a Kubernetes v1.11.3+ cluster.

### To Deploy on the cluster
**Build and push your image to the location specified by `IMG`:**

```sh
make docker-build docker-push IMG=<some-registry>/headscale-operator:tag
```

**NOTE:** This image ought to be published in the personal registry you specified.
And it is required to have access to pull the image from the working environment.
Make sure you have the proper permission to the registry if the above commands don't work.

**Install the CRDs into the cluster:**

```sh
make install
```

**Deploy the Manager to the cluster with the image specified by `IMG`:**

```sh
make deploy IMG=<some-registry>/headscale-operator:tag
```

> **NOTE**: If you encounter RBAC errors, you may need to grant yourself cluster-admin
privileges or be logged in as admin.

**Create instances of your solution**

Create a Headscale CR in your namespace:

```yaml
apiVersion: headscale.infrado.cloud/v1beta1
kind: Headscale
metadata:
  name: headscale-sample
  namespace: headscale
spec:
  version: "v0.27.0"
  replicas: 1
  config:
    server_url: http://vpn.headscale.local
    grpc_allow_insecure: true
    derp:
      server:
        enabled: false    
    disable_check_updates: false
    database:
      type: sqlite
    dns:
      magic_dns: false
  # Automatic API key management (optional)
  apiKey:
    autoManage: true        # Automatically create and rotate API keys
    secretName: headscale-api-key
    expiration: "2160h"     # API key expires in 90 days (2160 hours)
    rotationBuffer: "240h"  # Rotate 10 days (240 hours) before expiration
```

Apply the configuration:

```sh
kubectl apply -f config/samples/headscale_v1beta1_headscale.yaml
```

Or use kustomize:

```sh
kubectl apply -k config/samples/
```

The operator will automatically create:
- A StatefulSet running Headscale
- A ConfigMap with the Headscale configuration
- Services for HTTP, gRPC, and metrics endpoints
- PersistentVolumeClaims for data storage
- API key management sidecar (if enabled)
- Kubernetes Secret with the API key (if auto-managed)

### Accessing the API Key

When API key auto-management is enabled, the operator creates a Kubernetes Secret containing the API key:

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

### To Uninstall
**Delete the instances (CRs) from the cluster:**

```sh
kubectl delete -k config/samples/
```

**Delete the APIs(CRDs) from the cluster:**

```sh
make uninstall
```

**UnDeploy the controller from the cluster:**

```sh
make undeploy
```

## Project Distribution

Following the options to release and provide this solution to the users.

### By providing a bundle with all YAML files

1. Build the installer for the image built and published in the registry:

```sh
make build-installer IMG=<some-registry>/headscale-operator:tag
```

**NOTE:** The makefile target mentioned above generates an 'install.yaml'
file in the dist directory. This file contains all the resources built
with Kustomize, which are necessary to install this project without its
dependencies.

2. Using the installer

Users can just run 'kubectl apply -f <URL for YAML BUNDLE>' to install
the project, i.e.:

```sh
kubectl apply -f https://raw.githubusercontent.com/<org>/headscale-operator/<tag or branch>/dist/install.yaml
```

### By providing a Helm Chart

1. Build the chart using the optional helm plugin

```sh
kubebuilder edit --plugins=helm/v1-alpha
```

2. See that a chart was generated under 'dist/chart', and users
can obtain this solution from there.

**NOTE:** If you change the project, you need to update the Helm Chart
using the same command above to sync the latest changes. Furthermore,
if you create webhooks, you need to use the above command with
the '--force' flag and manually ensure that any custom configuration
previously added to 'dist/chart/values.yaml' or 'dist/chart/manager/manager.yaml'
is manually re-applied afterwards.

## Contributing
// TODO(user): Add detailed information on how you would like others to contribute to this project

**NOTE:** Run `make help` for more information on all potential `make` targets

More information can be found via the [Kubebuilder Documentation](https://book.kubebuilder.io/introduction.html)

## License

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

