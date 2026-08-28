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
