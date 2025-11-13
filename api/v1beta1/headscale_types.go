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

package v1beta1

import (
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NoiseConfig represents the Noise protocol configuration
type NoiseConfig struct {
	// PrivateKeyPath is the path to the Noise private key
	// +kubebuilder:default="/var/lib/headscale/noise_private.key"
	// +optional
	PrivateKeyPath string `json:"private_key_path,omitempty"`
}

// PrefixesConfig represents IP prefix configuration
type PrefixesConfig struct {
	// V4 is the IPv4 prefix for allocation
	// +kubebuilder:validation:Pattern=`^([0-9]{1,3}\.){3}[0-9]{1,3}/[0-9]{1,2}$`
	// +kubebuilder:default="100.64.0.0/10"
	// +optional
	V4 string `json:"v4,omitempty"`

	// V6 is the IPv6 prefix for allocation
	// +kubebuilder:validation:Pattern=`^([0-9a-fA-F]{0,4}:){2,7}([0-9a-fA-F]{0,4})/([0-9]{1,3})$`
	// +kubebuilder:default="fd7a:115c:a1e0::/48"
	// +optional
	V6 string `json:"v6,omitempty"`

	// Allocation strategy for IPs
	// +kubebuilder:validation:Enum=sequential;random
	// +kubebuilder:default="sequential"
	// +optional
	Allocation string `json:"allocation,omitempty"`
}

// DERPServerConfig represents embedded DERP server configuration
type DERPServerConfig struct {
	// Enabled indicates if the embedded DERP server is enabled
	// +kubebuilder:default=false
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// RegionID is the region ID for the embedded DERP server
	// +kubebuilder:default=999
	// +optional
	RegionID int `json:"region_id,omitempty"`

	// RegionCode is the region code for display
	// +kubebuilder:default="headscale"
	// +optional
	RegionCode string `json:"region_code,omitempty"`

	// RegionName is the region name for display
	// +kubebuilder:default="Headscale Embedded DERP"
	// +optional
	RegionName string `json:"region_name,omitempty"`

	// VerifyClients indicates whether to verify clients
	// +kubebuilder:default=true
	// +optional
	VerifyClients *bool `json:"verify_clients,omitempty"`

	// STUNListenAddr is the address for STUN connections
	// +kubebuilder:default="0.0.0.0:3478"
	// +optional
	STUNListenAddr string `json:"stun_listen_addr,omitempty"`

	// PrivateKeyPath is the path to the DERP server private key
	// +kubebuilder:default="/var/lib/headscale/derp_server_private.key"
	// +optional
	PrivateKeyPath string `json:"private_key_path,omitempty"`

	// AutomaticallyAddEmbeddedDerpRegion indicates whether to automatically add the embedded DERP region
	// +kubebuilder:default=true
	// +optional
	AutomaticallyAddEmbeddedDerpRegion *bool `json:"automatically_add_embedded_derp_region,omitempty"`

	// IPv4 is the public IPv4 address
	// +kubebuilder:default="198.51.100.1"
	// +optional
	IPv4 string `json:"ipv4,omitempty"`

	// IPv6 is the public IPv6 address
	// +kubebuilder:default="2001:db8::1"
	// +optional
	IPv6 string `json:"ipv6,omitempty"`
}

// DERPConfig represents DERP configuration
type DERPConfig struct {
	// Server configuration for embedded DERP server
	// +optional
	Server DERPServerConfig `json:"server"`

	// URLs is the list of external DERP map URLs
	// +kubebuilder:default={"https://controlplane.tailscale.com/derpmap/default"}
	// +optional
	URLs []string `json:"urls,omitempty"`

	// Paths is the list of local DERP map file paths
	// +optional
	Paths []string `json:"paths,omitempty"`

	// AutoUpdateEnabled indicates whether to auto-update DERP maps
	// +kubebuilder:default=true
	// +optional
	AutoUpdateEnabled *bool `json:"auto_update_enabled,omitempty"`

	// UpdateFrequency is how often to check for DERP updates
	// +kubebuilder:default="3h"
	// +optional
	UpdateFrequency string `json:"update_frequency,omitempty"`
}

// GormConfig represents GORM configuration
type GormConfig struct {
	// PrepareStmt enables prepared statements
	// +kubebuilder:default=true
	// +optional
	PrepareStmt *bool `json:"prepare_stmt,omitempty"`

	// ParameterizedQueries enables parameterized queries
	// +kubebuilder:default=true
	// +optional
	ParameterizedQueries *bool `json:"parameterized_queries,omitempty"`

	// SkipErrRecordNotFound skips "record not found" errors
	// +kubebuilder:default=true
	// +optional
	SkipErrRecordNotFound *bool `json:"skip_err_record_not_found,omitempty"`

	// SlowThreshold is the threshold for slow queries in milliseconds
	// +kubebuilder:default=1000
	// +optional
	SlowThreshold int `json:"slow_threshold,omitempty"`
}

// SqliteConfig represents SQLite configuration
type SqliteConfig struct {
	// Path is the path to the SQLite database file
	// +kubebuilder:default="/var/lib/headscale/db.sqlite"
	// +optional
	Path string `json:"path,omitempty"`

	// WriteAheadLog enables WAL mode
	// +kubebuilder:default=true
	// +optional
	WriteAheadLog *bool `json:"write_ahead_log,omitempty"`

	// WALAutocheckpoint sets the WAL autocheckpoint value
	// +kubebuilder:default=1000
	// +optional
	WALAutocheckpoint int `json:"wal_autocheckpoint,omitempty"`
}

// PostgresConfig represents PostgreSQL configuration
type PostgresConfig struct {
	// Host is the PostgreSQL host
	// +optional
	Host string `json:"host,omitempty"`

	// Port is the PostgreSQL port
	// +optional
	Port int `json:"port,omitempty"`

	// Name is the database name
	// +optional
	Name string `json:"name,omitempty"`

	// User is the database user
	// +optional
	User string `json:"user,omitempty"`

	// Pass is the database password
	// +optional
	Pass string `json:"pass,omitempty"`

	// MaxOpenConns is the maximum number of open connections
	// +kubebuilder:default=10
	// +optional
	MaxOpenConns int `json:"max_open_conns,omitempty"`

	// MaxIdleConns is the maximum number of idle connections
	// +kubebuilder:default=10
	// +optional
	MaxIdleConns int `json:"max_idle_conns,omitempty"`

	// ConnMaxIdleTimeSecs is the maximum connection idle time in seconds
	// +kubebuilder:default=3600
	// +optional
	ConnMaxIdleTimeSecs int `json:"conn_max_idle_time_secs,omitempty"`

	// SSL indicates whether to use SSL
	// +kubebuilder:default=false
	// +optional
	SSL *bool `json:"ssl,omitempty"`
}

// DatabaseConfig represents database configuration
type DatabaseConfig struct {
	// Type is the database type
	// +kubebuilder:validation:Enum=sqlite;postgres
	// +kubebuilder:default="sqlite"
	// +optional
	Type string `json:"type,omitempty"`

	// Debug enables debug mode
	// +kubebuilder:default=false
	// +optional
	Debug *bool `json:"debug,omitempty"`

	// Gorm configuration
	// +optional
	Gorm GormConfig `json:"gorm"`

	// Sqlite configuration
	// +optional
	Sqlite SqliteConfig `json:"sqlite"`

	// Postgres configuration
	// +optional
	Postgres PostgresConfig `json:"postgres"`
}

// TLSConfig represents TLS configuration
type TLSConfig struct {
	// LetsEncryptHostname is the hostname for Let's Encrypt
	// +optional
	LetsEncryptHostname string `json:"letsencrypt_hostname,omitempty"`

	// LetsEncryptCacheDir is the cache directory for Let's Encrypt
	// +kubebuilder:default="/var/lib/headscale/cache"
	// +optional
	LetsEncryptCacheDir string `json:"letsencrypt_cache_dir,omitempty"`

	// LetsEncryptChallengeType is the ACME challenge type
	// +kubebuilder:validation:Enum=HTTP-01;TLS-ALPN-01
	// +kubebuilder:default="HTTP-01"
	// +optional
	LetsEncryptChallengeType string `json:"letsencrypt_challenge_type,omitempty"`

	// LetsEncryptListen is the address for Let's Encrypt challenge
	// +kubebuilder:default=":http"
	// +optional
	LetsEncryptListen string `json:"letsencrypt_listen,omitempty"`

	// CertPath is the path to the TLS certificate
	// +optional
	CertPath string `json:"cert_path,omitempty"`

	// KeyPath is the path to the TLS key
	// +optional
	KeyPath string `json:"key_path,omitempty"`
}

// LogConfig represents logging configuration
type LogConfig struct {
	// Level is the log level
	// +kubebuilder:validation:Enum=panic;fatal;error;warn;info;debug;trace
	// +kubebuilder:default="info"
	// +optional
	Level string `json:"level,omitempty"`

	// Format is the log format
	// +kubebuilder:validation:Enum=text;json
	// +kubebuilder:default="text"
	// +optional
	Format string `json:"format,omitempty"`
}

// PolicyConfig represents ACL policy configuration
type PolicyConfig struct {
	// Mode is the policy mode
	// +kubebuilder:validation:Enum=file;database
	// +kubebuilder:default="file"
	// +optional
	Mode string `json:"mode,omitempty"`

	// Path is the path to the policy file
	// +optional
	Path string `json:"path,omitempty"`
}

// DNSNameserversConfig represents DNS nameservers configuration
type DNSNameserversConfig struct {
	// Global nameservers
	// +kubebuilder:default={"1.1.1.1", "1.0.0.1", "2606:4700:4700::1111", "2606:4700:4700::1001"}
	// +optional
	Global []string `json:"global,omitempty"`

	// Split DNS configuration
	// +optional
	Split map[string][]string `json:"split,omitempty"`
}

// DNSExtraRecord represents an extra DNS record
type DNSExtraRecord struct {
	// Name is the DNS record name
	// +optional
	Name string `json:"name,omitempty"`

	// Type is the DNS record type
	// +kubebuilder:validation:Enum=A;AAAA
	// +optional
	Type string `json:"type,omitempty"`

	// Value is the DNS record value
	// +optional
	Value string `json:"value,omitempty"`
}

// DNSConfig represents DNS configuration
type DNSConfig struct {
	// MagicDNS enables MagicDNS
	// +kubebuilder:default=true
	// +optional
	MagicDNS *bool `json:"magic_dns,omitempty"`

	// BaseDomain is the base domain for MagicDNS
	// +optional
	BaseDomain string `json:"base_domain,omitempty"`

	// OverrideLocalDNS overrides local DNS settings
	// +kubebuilder:default=true
	// +optional
	OverrideLocalDNS *bool `json:"override_local_dns,omitempty"`

	// Nameservers configuration
	// +optional
	Nameservers DNSNameserversConfig `json:"nameservers"`

	// SearchDomains is the list of search domains
	// +optional
	SearchDomains []string `json:"search_domains,omitempty"`

	// ExtraRecords is the list of extra DNS records
	// +optional
	ExtraRecords []DNSExtraRecord `json:"extra_records,omitempty"`
}

// PKCEConfig represents PKCE configuration
type PKCEConfig struct {
	// Enabled indicates if PKCE is enabled
	// +kubebuilder:default=false
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// Method is the PKCE method
	// +kubebuilder:validation:Enum=plain;S256
	// +kubebuilder:default="S256"
	// +optional
	Method string `json:"method,omitempty"`
}

// OIDCConfig represents OpenID Connect configuration
type OIDCConfig struct {
	// OnlyStartIfOIDCIsAvailable blocks startup until OIDC is available
	// +kubebuilder:default=true
	// +optional
	OnlyStartIfOIDCIsAvailable *bool `json:"only_start_if_oidc_is_available,omitempty"`

	// Issuer is the OIDC issuer URL
	// +optional
	Issuer string `json:"issuer,omitempty"`

	// ClientID is the OIDC client ID
	// +optional
	ClientID string `json:"client_id,omitempty"`

	// ClientSecret is the OIDC client secret
	// +optional
	ClientSecret string `json:"client_secret,omitempty"`

	// ClientSecretPath is the path to the OIDC client secret file
	// +optional
	ClientSecretPath string `json:"client_secret_path,omitempty"`

	// Expiry is the authentication expiry duration
	// +kubebuilder:default="180d"
	// +optional
	Expiry string `json:"expiry,omitempty"`

	// UseExpiryFromToken uses the token expiry
	// +kubebuilder:default=false
	// +optional
	UseExpiryFromToken *bool `json:"use_expiry_from_token,omitempty"`

	// Scope is the list of OIDC scopes
	// +optional
	Scope []string `json:"scope,omitempty"`

	// ExtraParams are additional parameters for the OIDC provider
	// +optional
	ExtraParams map[string]string `json:"extra_params,omitempty"`

	// AllowedDomains is the list of allowed email domains
	// +optional
	AllowedDomains []string `json:"allowed_domains,omitempty"`

	// AllowedUsers is the list of allowed email addresses
	// +optional
	AllowedUsers []string `json:"allowed_users,omitempty"`

	// AllowedGroups is the list of allowed groups
	// +optional
	AllowedGroups []string `json:"allowed_groups,omitempty"`

	// PKCE configuration
	// +optional
	PKCE PKCEConfig `json:"pkce"`
}

// LogTailConfig represents Logtail configuration
type LogTailConfig struct {
	// Enabled indicates if Logtail is enabled
	// +kubebuilder:default=false
	// +optional
	Enabled *bool `json:"enabled,omitempty"`
}

// PersistentVolumeClaimConfig represents the PVC configuration for Headscale data storage
type PersistentVolumeClaimConfig struct {
	// Size is the storage size for the PVC
	// +kubebuilder:default="128Mi"
	// +optional
	Size *resource.Quantity `json:"size,omitempty"`

	// StorageClassName is the storage class name for the PVC
	// +optional
	StorageClassName *string `json:"storage_class_name,omitempty"`
}

// HeadscaleConfig represents the complete Headscale configuration
type HeadscaleConfig struct {
	// ServerURL is the URL clients will connect to
	// +required
	ServerURL string `json:"server_url"`

	// ListenAddr is the address to listen on
	// +kubebuilder:default="0.0.0.0:8080"
	// +optional
	ListenAddr string `json:"listen_addr,omitempty"`

	// MetricsListenAddr is the address for metrics
	// +kubebuilder:default="0.0.0.0:9090"
	// +optional
	MetricsListenAddr string `json:"metrics_listen_addr,omitempty"`

	// GRPCListenAddr is the address for gRPC
	// +kubebuilder:default="0.0.0.0:50443"
	// +optional
	GRPCListenAddr string `json:"grpc_listen_addr,omitempty"`

	// GRPCAllowInsecure allows insecure gRPC
	// +kubebuilder:default=false
	// +optional
	GRPCAllowInsecure *bool `json:"grpc_allow_insecure,omitempty"`

	// Noise configuration
	// +optional
	Noise NoiseConfig `json:"noise"`

	// Prefixes configuration
	// +optional
	Prefixes PrefixesConfig `json:"prefixes"`

	// DERP configuration
	// +optional
	DERP DERPConfig `json:"derp"`

	// DisableCheckUpdates disables update checks
	// +kubebuilder:default=false
	// +optional
	DisableCheckUpdates *bool `json:"disable_check_updates,omitempty"`

	// EphemeralNodeInactivityTimeout is the timeout for ephemeral nodes
	// +kubebuilder:default="30m"
	// +optional
	EphemeralNodeInactivityTimeout string `json:"ephemeral_node_inactivity_timeout,omitempty"`

	// Database configuration
	// +optional
	Database DatabaseConfig `json:"database"`

	// ACMEURL is the ACME directory URL
	// +kubebuilder:default="https://acme-v02.api.letsencrypt.org/directory"
	// +optional
	ACMEURL string `json:"acme_url,omitempty"`

	// ACMEEmail is the email for ACME registration
	// +optional
	ACMEEmail string `json:"acme_email,omitempty"`

	// TLSLetsEncryptHostname is the hostname for Let's Encrypt
	// +optional
	TLSLetsEncryptHostname string `json:"tls_letsencrypt_hostname,omitempty"`

	// TLSLetsEncryptCacheDir is the cache directory for Let's Encrypt
	// +kubebuilder:default="/var/lib/headscale/cache"
	// +optional
	TLSLetsEncryptCacheDir string `json:"tls_letsencrypt_cache_dir,omitempty"`

	// TLSLetsEncryptChallengeType is the ACME challenge type
	// +kubebuilder:validation:Enum=HTTP-01;TLS-ALPN-01
	// +kubebuilder:default="HTTP-01"
	// +optional
	TLSLetsEncryptChallengeType string `json:"tls_letsencrypt_challenge_type,omitempty"`

	// TLSLetsEncryptListen is the address for Let's Encrypt challenge
	// +kubebuilder:default=":http"
	// +optional
	TLSLetsEncryptListen string `json:"tls_letsencrypt_listen,omitempty"`

	// TLSCertPath is the path to the TLS certificate
	// +optional
	TLSCertPath string `json:"tls_cert_path,omitempty"`

	// TLSKeyPath is the path to the TLS key
	// +optional
	TLSKeyPath string `json:"tls_key_path,omitempty"`

	// Log configuration
	// +optional
	Log LogConfig `json:"log"`

	// Policy configuration
	// +optional
	Policy PolicyConfig `json:"policy"`

	// DNS configuration
	// +optional
	DNS DNSConfig `json:"dns"`

	// UnixSocket is the path to the Unix socket
	// +kubebuilder:default="/var/run/headscale/headscale.sock"
	// +kubebuilder:validation:MinLength=1
	// +optional
	UnixSocket string `json:"unix_socket,omitempty"`

	// UnixSocketPermission is the Unix socket permission (e.g., "0770")
	// +kubebuilder:default="0770"
	// +optional
	UnixSocketPermission string `json:"unix_socket_permission,omitempty"`

	// OIDC configuration
	// +optional
	OIDC OIDCConfig `json:"oidc"`

	// LogTail configuration
	// +optional
	LogTail LogTailConfig `json:"logtail"`

	// RandomizeClientPort randomizes the WireGuard client port
	// +kubebuilder:default=false
	// +optional
	RandomizeClientPort *bool `json:"randomize_client_port,omitempty"`
}

// APIKeyConfig represents API key management configuration
type APIKeyConfig struct {
	// AutoManage enables automatic API key creation and rotation
	// +kubebuilder:default=true
	// +optional
	AutoManage *bool `json:"auto_manage,omitempty"`

	// SecretName is the name of the Kubernetes secret to store the API key
	// +kubebuilder:default="headscale-api-key"
	// +optional
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	SecretName string `json:"secret_name,omitempty"`

	// Expiration is the API key expiration duration in Go duration format (e.g., "2160h", "90d" is not valid, use "2160h" for 90 days)
	// The API key will be rotated before it expires
	// Examples: "720h" (30 days), "2160h" (90 days), "8760h" (365 days)
	// +kubebuilder:validation:Pattern=`^([0-9]+(\.[0-9]+)?(s|m|h))+$`
	// +kubebuilder:default="2160h"
	// +optional
	Expiration string `json:"expiration,omitempty"`

	// RotationBuffer is the time before expiration to rotate the key in Go duration format (e.g., "168h" for 7 days)
	// Key will be rotated when it has less than this time remaining
	// Examples: "168h" (7 days), "1920h" (80 days)
	// +kubebuilder:validation:Pattern=`^([0-9]+(\.[0-9]+)?(s|m|h))+$`
	// +kubebuilder:default="1920h"
	// +optional
	RotationBuffer string `json:"rotation_buffer,omitempty"`

	// ManagerImage is the container image to use for the API key manager sidecar
	// +kubebuilder:default="ghcr.io/infradohq/headscale-operator/apikey-manager:latest"
	// +optional
	ManagerImage string `json:"manager_image,omitempty"`
}

// HeadscaleSpec defines the desired state of Headscale
type HeadscaleSpec struct {
	// Version indicates the version of Headscale to deploy.
	// +kubebuilder:validation:Pattern=`^v?(\d+\.)?(\d+\.)?(\*|\d+)(-.+)?$`
	// +required
	Version string `json:"version"`

	// Image is the container image to use for Headscale.
	// +kubebuilder:default="headscale/headscale"
	// +kubebuilder:validation:MinLength=1
	// +optional
	Image string `json:"image,omitempty"`

	// Replicas indicates the number of Headscale instances to deploy.
	// +kubebuilder:validation:Minimum=0
	// +required
	Replicas int32 `json:"replicas"`

	// Config holds custom configuration for Headscale.
	// +optional
	Config HeadscaleConfig `json:"config"`

	// PersistentVolumeClaim configuration for data storage
	// +optional
	PersistentVolumeClaim PersistentVolumeClaimConfig `json:"persistent_volume_claim"`

	// APIKey configuration for automatic API key management
	// +optional
	APIKey APIKeyConfig `json:"api_key"`
}

// HeadscaleStatus defines the observed state of Headscale.
type HeadscaleStatus struct {
	// For Kubernetes API conventions, see:
	// https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties

	// conditions represent the current state of the Headscale resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	//
	// Standard condition types include:
	// - "Available": the resource is fully functional
	// - "Progressing": the resource is being created or updated
	// - "Degraded": the resource failed to reach or maintain its desired state
	//
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=hs
// +kubebuilder:validation:XValidation:rule="!has(self.spec.api_key.rotation_buffer) || !has(self.spec.api_key.expiration) || duration(self.spec.api_key.rotation_buffer) < duration(self.spec.api_key.expiration)",message="api_key.rotation_buffer must be less than api_key.expiration"

// Headscale is the Schema for the headscales API
type Headscale struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of Headscale
	// +required
	Spec HeadscaleSpec `json:"spec"`

	// status defines the observed state of Headscale
	// +optional
	Status HeadscaleStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// HeadscaleList contains a list of Headscale
type HeadscaleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Headscale `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Headscale{}, &HeadscaleList{})
}
