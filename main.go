/*
Copyright 2021.

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

package main

import (
	"context"
	"flag"
	"os"
	"time"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	"go.uber.org/zap/zapcore"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	ocinfrav1 "github.com/openshift/api/config/v1"
	hivev1 "github.com/openshift/hive/apis/hive/v1"
	backupv1beta1 "github.com/stolostron/cluster-backup-operator/api/v1beta1"
	"github.com/stolostron/cluster-backup-operator/controllers"
	certsv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/discovery"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	addonv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	workv1 "open-cluster-management.io/api/work/v1"
	chnv1 "open-cluster-management.io/multicloud-operators-channel/pkg/apis/apps/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	// operatorapiv1 "open-cluster-management.io/api/operator/v1"

	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	//+kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(backupv1beta1.AddToScheme(scheme))
	utilruntime.Must(chnv1.AddToScheme(scheme))
	utilruntime.Must(certsv1.AddToScheme(scheme))
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(clusterv1.AddToScheme(scheme))
	utilruntime.Must(workv1.AddToScheme(scheme))
	utilruntime.Must(addonv1alpha1.AddToScheme(scheme))
	utilruntime.Must(ocinfrav1.AddToScheme(scheme))
	// utilruntime.Must(operatorapiv1.AddToScheme(scheme)) Not adding since client it's remote
	utilruntime.Must(veleroapi.AddToScheme(scheme))
	utilruntime.Must(hivev1.AddToScheme(scheme))
	utilruntime.Must(corev1.AddToScheme(scheme))

	//+kubebuilder:scaffold:scheme
}

//nolint:funlen
func main() {
	var metricsAddr string
	var probeAddr string
	var enableLeaderElection bool
	var leaseDuration time.Duration
	var renewDeadline time.Duration
	var retryPeriod time.Duration

	flag.StringVar(
		&metricsAddr,
		"metrics-bind-address",
		":8080",
		"The address the metric endpoint binds to.",
	)
	flag.StringVar(
		&probeAddr,
		"health-probe-bind-address",
		":8081",
		"The address the probe endpoint binds to.",
	)
	flag.BoolVar(&enableLeaderElection, "leader-elect", true,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.DurationVar(&leaseDuration, "leader-election-lease-duration", 137*time.Second, ""+
		"The duration that non-leader candidates will wait after observing a leadership "+
		"renewal until attempting to acquire leadership of a led but unrenewed leader "+
		"slot. This is effectively the maximum duration that a leader can be stopped "+
		"before it is replaced by another candidate. This is only applicable if leader "+
		"election is enabled.")
	flag.DurationVar(&renewDeadline, "leader-election-renew-deadline", 107*time.Second, ""+
		"The interval between attempts by the acting master to renew a leadership slot "+
		"before it stops leading. This must be less than or equal to the lease duration. "+
		"This is only applicable if leader election is enabled.")
	flag.DurationVar(&retryPeriod, "leader-election-retry-period", 26*time.Second, ""+
		"The duration the clients should wait between attempting acquisition and renewal "+
		"of a leadership. This is only applicable if leader election is enabled.")

	opts := zap.Options{
		Development: true,
		TimeEncoder: zapcore.ISO8601TimeEncoder,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	setupLog.Info("Leader election settings", "enableLeaderElection", enableLeaderElection,
		"leaseDuration", leaseDuration,
		"renewDeadline", renewDeadline,
		"retryPeriod", retryPeriod)

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: metricsAddr,
		},
		WebhookServer: webhook.NewServer(webhook.Options{
			Port: 9443,
		}),
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "58497677.cluster.management.io",
		LeaseDuration:          &leaseDuration,
		RenewDeadline:          &renewDeadline,
		RetryPeriod:            &retryPeriod,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	cfg := mgr.GetConfig()

	// Check for VeleroCRDs - cannot start the controllers until the CRDs are present
	// (i.e. OADP has been installed)
	// Temp client for this check for CRDs before ctrl manager is started - no client cache
	setupClient, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		setupLog.Error(err, "error creating client")
		os.Exit(1)
	}
	for {
		crdCheckSuccessful, err := requiredCRDsPresent(context.Background(), setupClient)
		if err != nil {
			os.Exit(1)
		}

		if !crdCheckSuccessful {
			setupLog.Info("Velero CRDs are not installed, not starting BackupScheduleReconciler "+
				"or RestoreReconciler controllers", "crdCheckSuccessful", crdCheckSuccessful)
			time.Sleep(30 * time.Second)
			continue
		}

		// CRDs found
		setupLog.Info("Velero CRDs detected, starting controllers", "crdCheckSuccessful", crdCheckSuccessful)
		break
	}

	dc, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		setupLog.Error(err, "unable to set up discovery client")
		os.Exit(1)
	}

	// prepare the dynamic client
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		setupLog.Error(err, "unable to set up dynamic client")
		os.Exit(1)
	}

	if err = (&controllers.BackupScheduleReconciler{
		Client:          mgr.GetClient(),
		DiscoveryClient: dc,
		DynamicClient:   dyn,
		Scheme:          mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create Schedule controller")
		os.Exit(1)
	}

	kubeClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		kubeClient = nil
	}
	if err = (&controllers.RestoreReconciler{
		Client:          mgr.GetClient(),
		KubeClient:      kubeClient,
		DiscoveryClient: dc,
		DynamicClient:   dyn,
		Scheme:          mgr.GetScheme(),
		Recorder:        mgr.GetEventRecorderFor("Restore controller"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create Restore controller")
		os.Exit(1)
	}

	// Setup webhook for Restore validation
	if err = (&backupv1beta1.Restore{}).SetupWebhookWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create webhook", "webhook", "Restore")
		os.Exit(1)
	}
	//+kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func requiredCRDsPresent(ctx context.Context, k8sClient client.Client) (bool, error) {
	crdsPresent, err := controllers.VeleroCRDsPresent(ctx, k8sClient)
	if err != nil {
		setupLog.Error(err, "error querying k8s APIs")
		return false, err
	}

	return crdsPresent, nil
}
