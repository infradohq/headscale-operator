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
	"encoding/json"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/yaml"
)

// Defaults for the handful of headscale config keys the operator consumes itself
// to wire the Service, probes, and the apikey-manager sidecar. These mirror the
// operator's historical kubebuilder defaults and headscale's own defaults, so
// behavior is unchanged when a user omits them. Every other key in spec.config is
// passed through to headscale verbatim and validated by headscale at startup.
const (
	defaultListenAddr        = "0.0.0.0:8080"
	defaultMetricsListenAddr = "0.0.0.0:9090"
	defaultGRPCListenAddr    = "0.0.0.0:50443"
	defaultUnixSocket        = "/var/run/headscale/headscale.sock"
	defaultPolicyMode        = "file"
)

// headscaleConfigView is a partial, typed view over spec.config holding only the
// keys the operator reads for its own logic. spec.config is otherwise an opaque
// passthrough (runtime.RawExtension); this is how the controllers read the few
// values they depend on without re-coupling the CRD to headscale's schema.
type headscaleConfigView struct {
	ServerURL         string
	ListenAddr        string
	MetricsListenAddr string
	GRPCListenAddr    string
	UnixSocket        string
	PolicyMode        string
}

// rawConfigDoc mirrors the JSON shape of the keys parseConfigView extracts.
type rawConfigDoc struct {
	ServerURL         string `json:"server_url"`
	ListenAddr        string `json:"listen_addr"`
	MetricsListenAddr string `json:"metrics_listen_addr"`
	GRPCListenAddr    string `json:"grpc_listen_addr"`
	UnixSocket        string `json:"unix_socket"`
	Policy            struct {
		Mode string `json:"mode"`
	} `json:"policy"`
}

// parseConfigView extracts the operator-relevant keys from spec.config, filling
// in defaults for any that are unset. Unparseable config yields all-defaults
// (with an empty ServerURL); headscale itself reports the real parse error at
// startup and the operator surfaces it via status.
func parseConfigView(raw runtime.RawExtension) headscaleConfigView {
	var doc rawConfigDoc
	if len(raw.Raw) > 0 {
		_ = json.Unmarshal(raw.Raw, &doc)
	}

	return headscaleConfigView{
		ServerURL:         doc.ServerURL,
		ListenAddr:        firstNonEmpty(doc.ListenAddr, defaultListenAddr),
		MetricsListenAddr: firstNonEmpty(doc.MetricsListenAddr, defaultMetricsListenAddr),
		GRPCListenAddr:    firstNonEmpty(doc.GRPCListenAddr, defaultGRPCListenAddr),
		UnixSocket:        firstNonEmpty(doc.UnixSocket, defaultUnixSocket),
		PolicyMode:        firstNonEmpty(doc.Policy.Mode, defaultPolicyMode),
	}
}

// renderConfigYAML produces headscale's config.yaml from spec.config. Every
// user-provided key passes through unchanged; the operator only injects the
// networking/socket keys it must agree on (so the ConfigMap, Service, probes, and
// sidecar stay consistent). It deliberately injects nothing else, so keys removed
// by newer headscale versions are never re-added.
func renderConfigYAML(raw runtime.RawExtension, view headscaleConfigView) ([]byte, error) {
	doc := map[string]any{}
	if len(raw.Raw) > 0 {
		if err := json.Unmarshal(raw.Raw, &doc); err != nil {
			return nil, fmt.Errorf("failed to parse spec.config: %w", err)
		}
	}

	doc["listen_addr"] = view.ListenAddr
	doc["metrics_listen_addr"] = view.MetricsListenAddr
	doc["grpc_listen_addr"] = view.GRPCListenAddr
	doc["unix_socket"] = view.UnixSocket

	rendered, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal headscale config: %w", err)
	}
	return yaml.JSONToYAML(rendered)
}

func firstNonEmpty(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
