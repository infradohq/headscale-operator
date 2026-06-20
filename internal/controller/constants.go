package controller

// Status condition types and reasons shared across the Headscale controllers.
const (
	readyConditionType = "Ready"

	headscaleNotFoundReason = "HeadscaleNotFound"
	userNotFoundReason      = "UserNotFound"
	userNotReadyReason      = "UserNotReady"
)

// Container and volume names used in the rendered Headscale StatefulSet.
const (
	headscaleAppName           = "headscale"
	socketVolumeName           = "socket"
	apiKeyManagerContainerName = "apikey-manager"
)
