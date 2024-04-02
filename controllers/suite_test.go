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
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ocinfrav1 "github.com/openshift/api/config/v1"
	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	certsv1 "k8s.io/api/certificates/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	hivev1 "github.com/openshift/hive/apis/hive/v1"
	backupv1beta1 "github.com/stolostron/cluster-backup-operator/api/v1beta1"
	addonv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	operatorapiv1 "open-cluster-management.io/api/operator/v1"
	workv1 "open-cluster-management.io/api/work/v1"
	chnv1 "open-cluster-management.io/multicloud-operators-channel/pkg/apis/apps/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	discoveryclient "k8s.io/client-go/discovery"
	restclient "k8s.io/client-go/rest"
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
	appsInfoV1 := metav1.APIResourceList{
		GroupVersion: "apps.open-cluster-management.io/v1",
		APIResources: []metav1.APIResource{
			{Name: "channels", Namespaced: true, Kind: "Channel"},
			{Name: "subscriptions", Namespaced: true, Kind: "Subscription"},
		},
	}
	addonInfo := metav1.APIResourceList{
		GroupVersion: "addon.open-cluster-management.io/v1alpha1",
		APIResources: []metav1.APIResource{
			{Name: "managedclusteraddons", Namespaced: true, Kind: "ManagedClusterAddOn"},
		},
	}
	clusterv1beta1Info := metav1.APIResourceList{
		GroupVersion: "cluster.open-cluster-management.io/v1beta1",
		APIResources: []metav1.APIResource{
			{Name: "placements", Namespaced: true, Kind: "Placement"},
			{Name: "clustercurators", Namespaced: true, Kind: "ClusterCurator"},
			{Name: "managedclustersets", Namespaced: false, Kind: "ManagedClusterSet"},
			{Name: "backupschedules", Namespaced: true, Kind: "BackupSchedule"},
			{Name: "managedclusters", Namespaced: true, Kind: "ManagedCluster"},
		},
	}
	clusterv1Info := metav1.APIResourceList{
		GroupVersion: "cluster.open-cluster-management.io/v1",
		APIResources: []metav1.APIResource{
			{Name: "placements", Namespaced: true, Kind: "Placement"},
			{Name: "clustercurators", Namespaced: true, Kind: "ClusterCurator"},
			{Name: "managedclustersets", Namespaced: false, Kind: "ManagedClusterSet"},
			{Name: "backupschedules", Namespaced: true, Kind: "BackupSchedule"},
			{Name: "managedclusters", Namespaced: true, Kind: "ManagedCluster"},
		},
	}
	excluded := metav1.APIResourceList{
		GroupVersion: "proxy.open-cluster-management.io/v1beta1",
		APIResources: []metav1.APIResource{
			{Name: "managedclustermutators", Namespaced: false, Kind: "AdmissionReview"},
		},
	}
	hiveInfo := metav1.APIResourceList{
		GroupVersion: "hive.openshift.io/v1",
		APIResources: []metav1.APIResource{
			{Name: "clusterdeployments", Namespaced: false, Kind: "ClusterDeployment"},
			{Name: "dnszones", Namespaced: false, Kind: "DNSZone"},
			{Name: "clusterimageset", Namespaced: false, Kind: "ClusterImageSet"},
			{Name: "hiveconfig", Namespaced: false, Kind: "HiveConfig"},
		},
	}
	hiveExtraInfo := metav1.APIResourceList{
		GroupVersion: "other.hive.openshift.io/v1",
		APIResources: []metav1.APIResource{
			{Name: "clusterpools", Namespaced: true, Kind: "ClusterPool"},
		},
	}
	authAlpha1 := metav1.APIResourceList{
		GroupVersion: "authentication.open-cluster-management.io/v1beta1",
		APIResources: []metav1.APIResource{
			{Name: "managedserviceaccounts", Namespaced: true, Kind: "ManagedServiceAccount"},
		},
	}
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var list interface{}
		switch req.URL.Path {
		case "/apis/cluster.open-cluster-management.io/v1beta1":
			list = &clusterv1beta1Info
		case "/apis/cluster.open-cluster-management.io/v1":
			list = &clusterv1Info
		case "/apis/proxy.open-cluster-management.io/v1beta1":
			list = &excluded
		case "/apis/hive.openshift.io/v1":
			list = &hiveInfo
		case "/apis/other.hive.openshift.io/v1":
			list = &hiveExtraInfo
		case "/apis/hive.openshift.io/v1beta1":
			list = &hiveInfo
		case "/apis/authentication.open-cluster-management.io/v1beta1":
			list = authAlpha1
		case "/apis/apps.open-cluster-management.io/v1beta1":
			list = &appsInfo
		case "/apis/apps.open-cluster-management.io/v1":
			list = &appsInfoV1
		case "/apis/argoproj.io/v1alpha1":
			list = &argov1alphaInfo
		case "/apis/config.openshift.io/v1":
			list = &openshiftv1Info
		case "/apis/addon.open-cluster-management.io/v1alpha1":
			list = &addonInfo

		case "/api":
			list = &metav1.APIVersions{
				Versions: []string{
					"v1",
					"v1beta1",
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
								GroupVersion: "argoproj.io/v1",
								Version:      "v1",
							},
							{
								GroupVersion: "argoproj.io/v1beta1",
								Version:      "v1beta1",
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
						Name: "proxy.open-cluster-management.io",
						Versions: []metav1.GroupVersionForDiscovery{
							{
								GroupVersion: "proxy.open-cluster-management.io/v1beta1",
								Version:      "v1beta1",
							},
							{
								GroupVersion: "proxy.open-cluster-management.io/v1",
								Version:      "v1",
							},
						},
					},
					{
						Name: "hive.openshift.io",
						Versions: []metav1.GroupVersionForDiscovery{
							{GroupVersion: "hive.openshift.io/v1", Version: "v1"},
							{GroupVersion: "hive.openshift.io/v1beta1", Version: "v1beta1"},
						},
					},
					{
						Name: "other.hive.openshift.io",
						Versions: []metav1.GroupVersionForDiscovery{
							{GroupVersion: "other.hive.openshift.io/v1", Version: "v1"},
						},
					},
					{
						Name: "apps.open-cluster-management.io",
						Versions: []metav1.GroupVersionForDiscovery{
							{GroupVersion: "apps.open-cluster-management.io/v1beta1", Version: "v1beta1"},
							{GroupVersion: "apps.open-cluster-management.io/v1", Version: "v1"},
						},
					},
					{
						Name: "authentication.open-cluster-management.io",
						Versions: []metav1.GroupVersionForDiscovery{
							{GroupVersion: "authentication.open-cluster-management.io/v1beta1", Version: "v1beta1"},
						},
					},
					{
						Name: "addon.open-cluster-management.io",
						Versions: []metav1.GroupVersionForDiscovery{
							{GroupVersion: "addon.open-cluster-management.io/v1alpha1", Version: "v1alpha1"},
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
			resourcesList: &authAlpha1,
			path:          "/apis/authentication.open-cluster-management.io/v1beta1",
			request:       "authentication.open-cluster-management.io/v1beta1",
			expectErr:     false,
		},
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
			path:          "/apis/hive.openshift.io/v1",
			request:       "hive.openshift.io/v1",
			expectErr:     false,
		},
		{
			resourcesList: &hiveExtraInfo,
			path:          "/apis/other.hive.openshift.io/v1",
			request:       "other.hive.openshift.io/v1",
			expectErr:     false,
		},
		{
			resourcesList: &excluded,
			path:          "/apis/proxy.open-cluster-management.io/v1beta1",
			request:       "proxy.open-cluster-management.io/v1beta1",
			expectErr:     false,
		},
	}

	// check that these resources are backed up
	resourcesToBackup = []string{
		"placement.cluster.open-cluster-management.io",
	}

	//clusterpool.other.hive.openshift.io should go under managed cluster backup
	includedActivationAPIGroupsByName = []string{
		"other.hive.openshift.io",
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

	testEnvManagedCluster = &envtest.Environment{} // no CRDs for managedcluster
	managedClusterCfg, err := testEnvManagedCluster.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(managedClusterCfg).NotTo(BeNil())

	cfg, err := testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	err = ocinfrav1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = backupv1beta1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = backupv1beta1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = clusterv1.AddToScheme(scheme.Scheme) // for managedclusters
	Expect(err).NotTo(HaveOccurred())

	err = chnv1.AddToScheme(scheme.Scheme) // for channels
	Expect(err).NotTo(HaveOccurred())

	err = addonv1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = workv1.AddToScheme(scheme.Scheme)
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

	err = veleroapi.AddToScheme(scheme.Scheme) // for velero types
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

	res_channel_default := &unstructured.Unstructured{}
	res_channel_default.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "apps.open-cluster-management.io/v1beta1",
		"kind":       "Channel",
		"metadata": map[string]interface{}{
			"name":      "channel-new-default",
			"namespace": "default",
			"labels": map[string]interface{}{
				"velero.io/backup-name": "backup-name-aa",
			},
		},
		"spec": map[string]interface{}{
			"type":     "Git",
			"pathname": "https://github.com/test/app-samples",
		},
	})

	msaObj := &unstructured.Unstructured{}
	msaObj.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "authentication.open-cluster-management.io/v1beta1",
		"kind":       "ManagedServiceAccount",
		"metadata": map[string]interface{}{
			"name":      "auto-import-account",
			"namespace": "managed1",
			"labels": map[string]interface{}{
				msa_label: msa_service_name,
			},
		},
		"spec": map[string]interface{}{
			"somethingelse": "aaa",
			"rotation": map[string]interface{}{
				"validity": "50h",
				"enabled":  true,
			},
		},
	})

	// this is used by the auto-import updateMSAResources()
	msaObj2 := &unstructured.Unstructured{}
	msaObj2.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "authentication.open-cluster-management.io/v1beta1",
		"kind":       "ManagedServiceAccount",
		"metadata": map[string]interface{}{
			"name":      "auto-import-account",
			"namespace": "app",
			"labels": map[string]interface{}{
				msa_label: msa_service_name,
			},
		},
		"spec": map[string]interface{}{
			"somethingelse": "aaa",
			"rotation": map[string]interface{}{
				"validity": "50h",
				"enabled":  true,
			},
		},
	})

	msaGVK := schema.GroupVersionKind{Group: "authentication.open-cluster-management.io",
		Version: "v1beta1", Kind: "ManagedServiceAccount"}
	msaGVRList := schema.GroupVersionResource{Group: "authentication.open-cluster-management.io",
		Version: "v1beta1", Resource: "managedserviceaccounts"}

	//cluster version
	clsVGVK := schema.GroupVersionKind{Group: "config.openshift.io",
		Version: "v1", Kind: "ClusterVersion"}
	clsVGVKList := schema.GroupVersionResource{Group: "config.openshift.io",
		Version: "v1", Resource: "clusterversions"}

	clsvObj := &unstructured.Unstructured{}
	clsvObj.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "config.openshift.io/v1",
		"kind":       "ClusterVersion",
		"metadata": map[string]interface{}{
			"name": "version",
		},
		"spec": map[string]interface{}{
			"clusterID": "1234",
		},
	})

	// cluster deployments
	clsDGVK := schema.GroupVersionKind{Group: "hive.openshift.io",
		Version: "v1", Kind: "ClusterDeployment"}
	clsDGVKList := schema.GroupVersionResource{Group: "hive.openshift.io",
		Version: "v1", Resource: "clusterdeployments"}

	// cluster pools
	clsHiveObj := &unstructured.Unstructured{}
	clsHiveObj.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "other.hive.openshift.io/v1",
		"kind":       "ClusterPool",
		"metadata": map[string]interface{}{
			"name":      "vb-hive-1-worker",
			"namespace": "managed1",
		},
	})

	// placements
	plsGVK := schema.GroupVersionKind{Group: "cluster.open-cluster-management.io",
		Version: "v1beta1", Kind: "Placement"}
	plsGVKGVKList := schema.GroupVersionResource{Group: "cluster.open-cluster-management.io",
		Version: "v1beta1", Resource: "placements"}
	//curators
	crGVK := schema.GroupVersionKind{Group: "cluster.open-cluster-management.io",
		Version: "v1beta1", Kind: "ClusterCurator"}
	crGVKGVKList := schema.GroupVersionResource{Group: "cluster.open-cluster-management.io",
		Version: "v1beta1", Resource: "clustercurators"}
	//channels
	chGVK := schema.GroupVersionKind{Group: "apps.open-cluster-management.io",
		Version: "v1beta1", Kind: "Channel"}
	chGVKList := schema.GroupVersionResource{Group: "apps.open-cluster-management.io",
		Version: "v1beta1", Resource: "channels"}
	//subs
	subsGVK := schema.GroupVersionKind{Group: "apps.open-cluster-management.io",
		Version: "v1beta1", Kind: "Subscription"}
	subsGVKList := schema.GroupVersionResource{Group: "apps.open-cluster-management.io",
		Version: "v1beta1", Resource: "subscriptions"}

	//managed clusters
	clsGVK := schema.GroupVersionKind{Group: "cluster.open-cluster-management.io",
		Version: "v1beta1", Kind: "ManagedCluster"}
	clsGVKList := schema.GroupVersionResource{Group: "cluster.open-cluster-management.io",
		Version: "v1beta1", Resource: "managedclusters"}
	//managed clusters sets
	clsSGVK := schema.GroupVersionKind{Group: "cluster.open-cluster-management.io",
		Version: "v1beta1", Kind: "ManagedClusterSet"}
	clsSGVKList := schema.GroupVersionResource{Group: "cluster.open-cluster-management.io",
		Version: "v1beta1", Resource: "managedclustersets"}
	//backups
	bsSGVK := schema.GroupVersionKind{Group: "cluster.open-cluster-management.io",
		Version: "v1beta1", Kind: "BackupSchedule"}
	bsSGVKList := schema.GroupVersionResource{Group: "cluster.open-cluster-management.io",
		Version: "v1beta1", Resource: "backupschedules"}
	//mutators
	mGVK := schema.GroupVersionKind{Group: "proxy.open-cluster-management.io",
		Version: "v1beta1", Kind: "AdmissionReview"}
	mVKList := schema.GroupVersionResource{Group: "proxy.open-cluster-management.io",
		Version: "v1beta1", Resource: "managedclustermutators"}
	//pools
	cpGVK := schema.GroupVersionKind{Group: "other.hive.openshift.io",
		Version: "v1", Kind: "ClusterPool"}
	cpVKList := schema.GroupVersionResource{Group: "other.hive.openshift.io",
		Version: "v1", Resource: "clusterpools"}
	//dns
	dnsGVK := schema.GroupVersionKind{Group: "hive.openshift.io",
		Version: "v1", Kind: "DNSZone"}
	dnsVKList := schema.GroupVersionResource{Group: "hive.openshift.io",
		Version: "v1", Resource: "dnszones"}
	//image set
	imgGVK := schema.GroupVersionKind{Group: "hive.openshift.io",
		Version: "v1", Kind: "ClusterImageSet"}
	imgVKList := schema.GroupVersionResource{Group: "hive.openshift.io",
		Version: "v1", Resource: "clusterimageset"}
	//hive config
	hGVK := schema.GroupVersionKind{Group: "hive.openshift.io",
		Version: "v1", Kind: "HiveConfig"}
	hVKList := schema.GroupVersionResource{Group: "hive.openshift.io",
		Version: "v1", Resource: "hiveconfig"}

	//addon
	aoGVK := schema.GroupVersionKind{Group: "addon.open-cluster-management.io",
		Version: "v1alpha1", Kind: "ManagedClusterAddOn"}
	aoVKList := schema.GroupVersionResource{Group: "addon.open-cluster-management.io",
		Version: "v1alpha1", Resource: "managedclusteraddons"}

	///
	gvrToListKindR := map[schema.GroupVersionResource]string{
		msaGVRList:    "ManagedServiceAccountList",
		clsVGVKList:   "ClusterVersionList",
		clsDGVKList:   "ClusterDeploymentList",
		plsGVKGVKList: "PlacementList",
		crGVKGVKList:  "ClusterCuratorList",
		chGVKList:     "ChannelList",
		clsGVKList:    "ManagedClusterList",
		clsSGVKList:   "ManagedClusterSetList",
		bsSGVKList:    "BackupScheduleList",
		mVKList:       "AdmissionReviewList",
		cpVKList:      "ClusterPoolList",
		dnsVKList:     "DNSZoneList",
		imgVKList:     "ClusterImageSetList",
		hVKList:       "HiveConfigList",
		subsGVKList:   "SubscriptionList",
		aoVKList:      "ManagedClusterAddOnList",
	}

	unstructuredSchemeR := runtime.NewScheme()
	unstructuredSchemeR.AddKnownTypes(msaGVK.GroupVersion(), msaObj)
	unstructuredSchemeR.AddKnownTypes(msaGVK.GroupVersion(), msaObj2)
	unstructuredSchemeR.AddKnownTypes(clsVGVK.GroupVersion(), clsvObj)
	unstructuredSchemeR.AddKnownTypes(clsDGVK.GroupVersion())
	unstructuredSchemeR.AddKnownTypes(plsGVK.GroupVersion())
	unstructuredSchemeR.AddKnownTypes(crGVK.GroupVersion())
	unstructuredSchemeR.AddKnownTypes(chGVK.GroupVersion(), res_channel_default)
	unstructuredSchemeR.AddKnownTypes(clsGVK.GroupVersion())
	unstructuredSchemeR.AddKnownTypes(clsSGVK.GroupVersion())
	unstructuredSchemeR.AddKnownTypes(bsSGVK.GroupVersion())
	unstructuredSchemeR.AddKnownTypes(mGVK.GroupVersion())
	unstructuredSchemeR.AddKnownTypes(cpGVK.GroupVersion(), clsHiveObj)
	unstructuredSchemeR.AddKnownTypes(dnsGVK.GroupVersion())
	unstructuredSchemeR.AddKnownTypes(imgGVK.GroupVersion())
	unstructuredSchemeR.AddKnownTypes(hGVK.GroupVersion())
	unstructuredSchemeR.AddKnownTypes(subsGVK.GroupVersion())
	unstructuredSchemeR.AddKnownTypes(aoGVK.GroupVersion())

	dynR := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(unstructuredSchemeR,
		gvrToListKindR,
		msaObj, msaObj2, clsvObj, res_channel_default, clsHiveObj)

	//create some resources
	dynR.Resource(chGVKList).Namespace("default").Create(context.Background(),
		res_channel_default, v1.CreateOptions{})
	//
	dynR.Resource(msaGVRList).Namespace("managed1").Create(context.Background(),
		msaObj, v1.CreateOptions{})
	dynR.Resource(msaGVRList).Namespace("app").Create(context.Background(),
		msaObj2, v1.CreateOptions{})
	dynR.Resource(cpVKList).Namespace("managed1").Create(context.Background(),
		clsHiveObj, v1.CreateOptions{})

	err = (&RestoreReconciler{
		KubeClient:      nil,
		Client:          mgr.GetClient(),
		Scheme:          mgr.GetScheme(),
		DiscoveryClient: fakeDiscovery,
		DynamicClient:   dynR,
		Recorder:        mgr.GetEventRecorderFor("restore reconciler"),
	}).SetupWithManager(mgr)
	Expect(err).ToNot(HaveOccurred())

	err = (&BackupScheduleReconciler{
		Client:          mgr.GetClient(),
		Scheme:          mgr.GetScheme(),
		DiscoveryClient: fakeDiscovery,
		DynamicClient:   dynR,
	}).SetupWithManager(mgr)
	Expect(err).ToNot(HaveOccurred())

	go func() {
		err = mgr.Start(ctrl.SetupSignalHandler())
		Expect(err).ToNot(HaveOccurred())
	}()

})

var _ = AfterSuite(func() {
	By("tearing down the test environment")

	defer testEnv.Stop()

	defer testEnvManagedCluster.Stop()

	defer server.Close()
})
