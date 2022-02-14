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

package controllers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ocinfrav1 "github.com/openshift/api/config/v1"
	certsv1 "k8s.io/api/certificates/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/restmapper"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	hivev1 "github.com/openshift/hive/apis/hive/v1"
	backupv1beta1 "github.com/stolostron/cluster-backup-operator/api/v1beta1"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	operatorapiv1 "open-cluster-management.io/api/operator/v1"
	chnv1 "open-cluster-management.io/multicloud-operators-channel/pkg/apis/apps/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	discoveryclient "k8s.io/client-go/discovery"
	restclient "k8s.io/client-go/rest"

	valeroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	//+kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var k8sClient client.Client
var testEnv *envtest.Environment

var managedClusterK8sClient client.Client
var testEnvManagedCluster *envtest.Environment
var fakeDiscovery *discoveryclient.DiscoveryClient
var server *httptest.Server
var resourcesToBackup []string

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)

	// fetch the current config
	suiteConfig, reporterConfig := GinkgoConfiguration()
	// pass it in to RunSpecs
	RunSpecs(t, "Controller Suite", suiteConfig, reporterConfig)
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}

	argov1alphaInfo := metav1.APIResourceList{
		GroupVersion: "argoproj.io/v1alpha1",
		APIResources: []metav1.APIResource{
			{Name: "applications", Namespaced: true, Kind: "Application"},
			{Name: "applicationsets", Namespaced: true, Kind: "ApplicationSet"},
			{Name: "argocds", Namespaced: true, Kind: "Argocd"},
		},
	}
	openshiftv1Info :=
		metav1.APIResourceList{
			GroupVersion: "config.openshift.io/v1",
			APIResources: []metav1.APIResource{
				{Name: "clusterversions", Namespaced: false, Kind: "ClusterVersion"},
			},
		}
	appsInfo := metav1.APIResourceList{
		GroupVersion: "apps.open-cluster-management.io/v1beta1",
		APIResources: []metav1.APIResource{
			{Name: "channels", Namespaced: true, Kind: "Channel"},
			{Name: "subscriptions", Namespaced: true, Kind: "Subscription"},
		},
	}
	clusterv1beta1Info := metav1.APIResourceList{
		GroupVersion: "cluster.open-cluster-management.io/v1beta1",
		APIResources: []metav1.APIResource{
			{Name: "placements", Namespaced: true, Kind: "Placement"},
			{Name: "clustercurators", Namespaced: true, Kind: "ClusterCurator"},
			{Name: "backupschedules", Namespaced: true, Kind: "BackupSchedule"},
			{Name: "managedclusters", Namespaced: true, Kind: "ManagedCluster"},
		},
	}
	clusterv1Info := metav1.APIResourceList{
		GroupVersion: "cluster.open-cluster-management.io/v1",
		APIResources: []metav1.APIResource{
			{Name: "placements", Namespaced: true, Kind: "Placement"},
			{Name: "clustercurators", Namespaced: true, Kind: "ClusterCurator"},
			{Name: "backupschedules", Namespaced: true, Kind: "BackupSchedule"},
			{Name: "managedclusters", Namespaced: true, Kind: "ManagedCluster"},
		},
	}
	excluded := metav1.APIResourceList{
		GroupVersion: "admission.cluster.open-cluster-management.io/v1beta1",
		APIResources: []metav1.APIResource{
			{Name: "managedclustermutators", Namespaced: false, Kind: "AdmissionReview"},
		},
	}
	hiveInfo := metav1.APIResourceList{
		GroupVersion: "hive.openshift.io/v1beta1",
		APIResources: []metav1.APIResource{
			{Name: "dnszones", Namespaced: false, Kind: "DNSZone"},
		},
	}
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var list interface{}
		switch req.URL.Path {
		case "/apis/cluster.open-cluster-management.io/v1beta1":
			list = &clusterv1beta1Info
		case "/apis/cluster.open-cluster-management.io/v1":
			list = &clusterv1Info
		case "/apis/admission.cluster.open-cluster-management.io/v1beta1":
			list = &excluded
		case "/apis/hive.openshift.io/v1beta1":
			list = &hiveInfo
		case "/apis/apps.open-cluster-management.io/v1beta1":
			list = &appsInfo
		case "/apis/argoproj.io/v1alpha1":
			list = &argov1alphaInfo
		case "/apis/config.openshift.io/v1":
			list = &openshiftv1Info

		case "/api":
			list = &metav1.APIVersions{
				Versions: []string{
					"v1",
				},
			}
		case "/apis":
			list = &metav1.APIGroupList{
				Groups: []metav1.APIGroup{
					{
						Name: "config.openshift.io",
						Versions: []metav1.GroupVersionForDiscovery{
							{
								GroupVersion: "config.openshift.io/v1",
								Version:      "v1",
							},
						},
					},
					{
						Name: "argoproj.io",
						Versions: []metav1.GroupVersionForDiscovery{
							{
								GroupVersion: "argoproj.io/v1alpha1",
								Version:      "v1alpha1",
							},
						},
					},
					{
						Name: "cluster.open-cluster-management.io",
						Versions: []metav1.GroupVersionForDiscovery{
							{
								GroupVersion: "cluster.open-cluster-management.io/v1beta1",
								Version:      "v1beta1",
							},
							{
								GroupVersion: "cluster.open-cluster-management.io/v1",
								Version:      "v1",
							},
						},
					},
					{
						Name: "admission.cluster.open-cluster-management.io",
						Versions: []metav1.GroupVersionForDiscovery{
							{
								GroupVersion: "admission.cluster.open-cluster-management.io/v1beta1",
								Version:      "v1beta1",
							},
						},
					},
					{
						Name: "hive.openshift.io",
						Versions: []metav1.GroupVersionForDiscovery{
							{GroupVersion: "hive.openshift.io/v1beta1", Version: "v1beta1"},
						},
					},
					{
						Name: "apps.open-cluster-management.io",
						Versions: []metav1.GroupVersionForDiscovery{
							{GroupVersion: "apps.open-cluster-management.io/v1beta1", Version: "v1beta1"},
						},
					},
				},
			}
		default:
			//t.Logf("unexpected request: %s", req.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		output, err := json.Marshal(list)
		if err != nil {
			//t.Errorf("unexpected encoding error: %v", err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(output)
	}))

	fakeDiscovery = discoveryclient.NewDiscoveryClientForConfigOrDie(
		&restclient.Config{Host: server.URL},
	)

	tests := []struct {
		resourcesList *metav1.APIResourceList
		path          string
		request       string
		expectErr     bool
	}{
		{
			resourcesList: &clusterv1beta1Info,
			path:          "/apis/cluster.open-cluster-management.io/v1beta1",
			request:       "cluster.open-cluster-management.io/v1beta1",
			expectErr:     false,
		},
		{
			resourcesList: &clusterv1Info,
			path:          "/apis/cluster.open-cluster-management.io/v1",
			request:       "cluster.open-cluster-management.io/v1",
			expectErr:     false,
		},
		{
			resourcesList: &argov1alphaInfo,
			path:          "/apis/argoproj.io/v1alpha1",
			request:       "argoproj.io/v1alpha1",
			expectErr:     false,
		},
		{
			resourcesList: &openshiftv1Info,
			path:          "/apis/config.openshift.io/v1",
			request:       "config.openshift.io/v1",
			expectErr:     false,
		},
		{
			resourcesList: &hiveInfo,
			path:          "/apis/hive.openshift.io/v1beta1",
			request:       "hive.openshift.io/v1beta1",
			expectErr:     false,
		},
		{
			resourcesList: &excluded,
			path:          "/apis/admission.cluster.open-cluster-management.io/v1beta1",
			request:       "admission.cluster.open-cluster-management.io/v1beta1",
			expectErr:     false,
		},
	}

	// check that these resources are backed up
	resourcesToBackup = []string{
		"clusterdeployment.hive.openshift.io",
		"machinepool.hive.openshift.io",
		"placement.cluster.open-cluster-management.io",
	}
	test := tests[1]
	_, err := fakeDiscovery.ServerResourcesForGroupVersion(test.request)

	fakeDiscovery := discoveryclient.NewDiscoveryClientForConfigOrDie(
		&restclient.Config{Host: server.URL},
	)
	got, err := fakeDiscovery.ServerResourcesForGroupVersion(test.request)

	if test.expectErr {
		Expect(err).NotTo(BeNil())
	}
	Expect(reflect.DeepEqual(got, test.resourcesList)).To(BeTrue())

	_, err2 := fakeDiscovery.ServerGroups()
	Expect(err2).To(BeNil())

	dyn, err := dynamic.NewForConfig(&restclient.Config{Host: server.URL})
	Expect(err).To(BeNil())

	testEnvManagedCluster = &envtest.Environment{} // no CRDs for managedcluster
	managedClusterCfg, err := testEnvManagedCluster.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(managedClusterCfg).NotTo(BeNil())

	cfg, err := testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	err = backupv1beta1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = clusterv1.AddToScheme(scheme.Scheme) // for managedclusters
	Expect(err).NotTo(HaveOccurred())

	err = chnv1.AddToScheme(scheme.Scheme) // for channels
	Expect(err).NotTo(HaveOccurred())

	err = hivev1.AddToScheme(scheme.Scheme) // for clusterpools
	Expect(err).NotTo(HaveOccurred())

	err = certsv1.AddToScheme(scheme.Scheme) // for CSR
	Expect(err).NotTo(HaveOccurred())

	err = ocinfrav1.AddToScheme(scheme.Scheme) // for openshift config infrastructure types
	Expect(err).NotTo(HaveOccurred())

	err = operatorapiv1.AddToScheme(scheme.Scheme) // for Klusterlet CRD
	Expect(err).NotTo(HaveOccurred())

	err = rbacv1.AddToScheme(scheme.Scheme) // for clusterroles and clusterrolebindings
	Expect(err).NotTo(HaveOccurred())

	err = valeroapi.AddToScheme(scheme.Scheme) // for velero types
	Expect(err).NotTo(HaveOccurred())

	//+kubebuilder:scaffold:scheme

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	managedClusterK8sClient, err = client.New(
		managedClusterCfg,
		client.Options{Scheme: scheme.Scheme},
	)
	Expect(err).NotTo(HaveOccurred())
	Expect(managedClusterK8sClient).NotTo(BeNil())

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
	})
	Expect(err).ToNot(HaveOccurred())
	Expect(mgr).NotTo(BeNil())

	mapper := restmapper.NewDeferredDiscoveryRESTMapper(
		memory.NewMemCacheClient(fakeDiscovery),
	)

	err = (&RestoreReconciler{
		KubeClient:      nil,
		Client:          mgr.GetClient(),
		Scheme:          mgr.GetScheme(),
		DiscoveryClient: fakeDiscovery,
		DynamicClient:   dyn,
		RESTMapper:      mapper,
		Recorder:        mgr.GetEventRecorderFor("restore reconciler"),
	}).SetupWithManager(mgr)
	Expect(err).ToNot(HaveOccurred())

	err = (&BackupScheduleReconciler{
		Client:          mgr.GetClient(),
		Scheme:          mgr.GetScheme(),
		DiscoveryClient: fakeDiscovery,
		DynamicClient:   dyn,
		RESTMapper:      mapper,
	}).SetupWithManager(mgr)
	Expect(err).ToNot(HaveOccurred())

	go func() {
		err = mgr.Start(ctrl.SetupSignalHandler())
		Expect(err).ToNot(HaveOccurred())
	}()

})

var _ = AfterSuite(func() {
	By("tearing down the test environment")

	err := testEnvManagedCluster.Stop()
	Expect(err).NotTo(HaveOccurred())
	err = testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())

	defer server.Close()
})
