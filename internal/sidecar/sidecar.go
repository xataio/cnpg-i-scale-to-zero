package sidecar

import (
	"context"
	"fmt"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/cloudnative-pg/machinery/pkg/log"
	"github.com/spf13/viper"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/scheme"

	"github.com/xataio/cnpg-i-scale-to-zero/pkg/metadata"
)

// Start starts the sidecar informers and CNPG-i server
func Start(ctx context.Context) error {
	scheme := generateScheme(ctx)
	setupLog := log.FromContext(ctx)

	podName := viper.GetString("pod-name")
	clusterName := viper.GetString("cluster-name")
	namespace := viper.GetString("namespace")

	setupLog.Info("Starting scale to zero plugin sidecar", "podName", podName, "clusterName", clusterName, "namespace", namespace, "version", metadata.Data.Version)

	clientOptions := client.Options{
		Scheme: scheme,
		Cache: &client.CacheOptions{
			DisableFor: []client.Object{
				&corev1.Secret{},
				&cnpgv1.Cluster{},
			},
		},
	}

	c, err := client.New(ctrl.GetConfigOrDie(), clientOptions)
	if err != nil {
		setupLog.Error(err, "unable to create client")
		return err
	}

	scaleToZeroSidecar, err := newScaleToZero(ctx, config{
		podName: podName,
		clusterKey: types.NamespacedName{
			Namespace: namespace,
			Name:      clusterName,
		},
	}, c)
	if err != nil {
		return fmt.Errorf("failed to create scale to zero sidecar: %w", err)
	}

	err = scaleToZeroSidecar.Start(ctx)
	defer scaleToZeroSidecar.Stop(ctx)

	return err
}

// generateScheme creates a runtime.Scheme object with all the
// definition needed to support the sidecar. This allows
// the plugin to be used in every CNPG-based operator.
func generateScheme(ctx context.Context) *runtime.Scheme {
	result := runtime.NewScheme()

	utilruntime.Must(clientgoscheme.AddToScheme(result))

	cnpgGroup := viper.GetString("custom-cnpg-group")
	cnpgVersion := viper.GetString("custom-cnpg-version")
	if len(cnpgGroup) == 0 {
		cnpgGroup = cnpgv1.SchemeGroupVersion.Group
	}
	if len(cnpgVersion) == 0 {
		cnpgVersion = cnpgv1.SchemeGroupVersion.Version
	}

	// Proceed with custom registration of the CNPG scheme
	schemeGroupVersion := schema.GroupVersion{Group: cnpgGroup, Version: cnpgVersion}
	schemeBuilder := &scheme.Builder{GroupVersion: schemeGroupVersion}
	schemeBuilder.Register(
		&cnpgv1.Cluster{}, &cnpgv1.ClusterList{},
		&cnpgv1.ScheduledBackup{}, &cnpgv1.ScheduledBackupList{})
	utilruntime.Must(schemeBuilder.AddToScheme(result))

	schemeLog := log.FromContext(ctx)
	schemeLog.Info("CNPG types registration", "schemeGroupVersion", schemeGroupVersion)

	return result
}
