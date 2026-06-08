package scaletozero

import (
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	cnpgutils "github.com/cloudnative-pg/cloudnative-pg/pkg/utils"
)

const (
	HealthyClusterStatus = cnpgv1.PhaseHealthy

	HibernationAnnotation        = cnpgutils.HibernationAnnotationName
	HibernationAnnotationValueOn = string(cnpgutils.HibernationAnnotationValueOn)
	ClusterLabel                 = cnpgutils.ClusterLabelName

	EnabledAnnotation     = "xata.io/scale-to-zero-enabled"
	EnabledAnnotationTrue = "true"
	InactivityAnnotation  = "xata.io/scale-to-zero-inactivity-minutes"
	SidecarLabel          = "xata.io/scale-to-zero-sidecar"
	SidecarLabelTrue      = "true"

	DefaultInactivityMinutes = 30
)
