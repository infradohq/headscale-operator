package controller

// Shared string constants used across the controller test suites.
const (
	namespace = "default"

	testHeadscaleVersion = "v0.29.1"
	nonExistentResource  = "non-existent-resource"
	nonExistentHeadscale = "non-existent-headscale"
	extraVolumeName      = "extra-vol"
	routerCIDR           = "10.0.0.0/8"
	routerTag            = "tag:router"
	testAPIKeySecret     = "test-api-key-secret"
	testAPIKeySecretPAK  = "test-api-key-secret-pak"
)
