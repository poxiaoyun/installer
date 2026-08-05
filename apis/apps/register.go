package apps

const (
	// GroupName is the group name used in this package.
	GroupName     = "apps.xiaoshiai.cn"
	FinalizerName = GroupName + "/finalizer"
)

const (
	// LabelInstance identifies resources and Pods that belong to an Instance.
	LabelInstance = "app.kubernetes.io/instance"
	// LabelExposeNodeIP selects nodes whose IPs may be exposed in resolved endpoints.
	LabelExposeNodeIP = "cloud.xiaoshiai.cn/expose-node-ip"
)

const (
	AnnotationStatesExpression              = "app.kubernetes.io/states-expression"
	AnnotationSummaryExpression             = "app.kubernetes.io/summary-expression"
	AnnotationEndpointsExpression           = "app.kubernetes.io/endpoints-expression"
	AnnotationAdditionalEndpointsExpression = "app.kubernetes.io/additional-endpoints-expression"
	AnnotationScalePodSelector              = "app.kubernetes.io/scale-pod-selector"
	AnnotationUpgradeStrategy               = "app.kubernetes.io/upgrade-strategy"
	AnnotationRemoveStrategy                = "app.kubernetes.io/remove-strategy"
	AnnotationIngressPorts                  = "cloud.xiaoshiai.cn/ingress-ports"
	AnnotationAllowClusterScoped            = GroupName + "/allow-cluster-scoped"
)

const (
	ChartSecretType         = "apps.xiaoshiai.cn/helm-chart.v1"
	ContentDigestAnnotation = "apps.xiaoshiai.cn/content-digest"
	ChartSecretKey          = "chart.tgz"
)

const (
	StateStatusDegraded = "Degraded"
	StateStatusUpdating = "Updating"
	StateStatusScaling  = "Scaling"

	StateStatusPaused  = "Paused"
	StateStatusUnknown = "Unknown"

	StateStatusPending          = "Pending"
	StateStatusCrashLoopBackOff = "CrashLoopBackOff"
	StateStatusFailed           = "Failed"
	StateStatusUnhealthy        = "Unhealthy"
	StateStatusError            = "Error"

	StateStatusSucceeded    = "Succeeded"
	StateStatusActive       = "Active"
	StateStatusHealthy      = "Healthy"
	StateStatusScaledToZero = "ScaledToZero"
	StateStatusCompleted    = "Completed"
	StateStatusRunning      = "Running"
)
