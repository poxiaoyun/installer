package controller

const NodeIPPlaceholder = "{NodeIP}"

// Condition reasons emitted by the Instance controller.
const (
	ReasonScaleObservationFailed     = "ScaleObservationFailed"
	ReasonInvalidScalePodSelector    = "InvalidScalePodSelector"
	ReasonAutoscalingReady           = "AutoscalingReady"
	ReasonPaused                     = "Paused"
	ReasonReady                      = "Ready"
	ReasonExpressionEvaluationFailed = "ExpressionEvaluationFailed"
	ReasonExpressionsReady           = "ExpressionsReady"
	ReasonEndpointResolutionFailed   = "EndpointResolutionFailed"
	ReasonEndpointsReady             = "EndpointsReady"
	ReasonUninstallFailed            = "UninstallFailed"
	ReasonDependencyNotReady         = "DependencyNotReady"
	ReasonDependencyCheckFailed      = "DependencyCheckFailed"
	ReasonAllDependenciesReady       = "AllDependenciesReady"
	ReasonInvalidSource              = "InvalidSource"
	ReasonResolveValuesFailed        = "ResolveValuesFailed"
	ReasonResolveAuthFailed          = "ResolveAuthFailed"
	ReasonResolveTLSFailed           = "ResolveTLSFailed"
	ReasonInstalled                  = "Installed"
	ReasonApplyFailed                = "ApplyFailed"
)
