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
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"
	"time"

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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	discoveryclient "k8s.io/client-go/discovery"
	restclient "k8s.io/client-go/rest"
	//+kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

// Global test environment variables
// testEnv: Creates a lightweight, local Kubernetes API server for testing controllers
// without requiring a full cluster. This provides an isolated testing environment
// with real Kubernetes API behavior.
var k8sClient client.Client
var testEnv *envtest.Environment

// Mock components for testing API discovery and resource management
// fakeDiscovery: Simulates Kubernetes API discovery for testing resource lookups
// server: HTTP test server that mocks Kubernetes API endpoints
var fakeDiscovery *discoveryclient.DiscoveryClient
var server *httptest.Server

// Test configuration variables
var resourcesToBackup []string
var cancel context.CancelFunc
var managerDone chan struct{} // Channel to signal when manager has stopped

// Test constants
const (
	testClusterID    = "1234"
	testNamespace    = "managed1"
	testAppNamespace = "app"
	testBackupName   = "backup-name-aa"
	testMCHName      = "cluster-backup" // Used by InternalHubComponent
	testServiceName  = "auto-import-service"
)

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)

	// fetch the current config
	suiteConfig, reporterConfig := GinkgoConfiguration()
	// pass it in to RunSpecs
	RunSpecs(t, "Controller Suite", suiteConfig, reporterConfig)
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	By("setting up test environment")

	// STEP 1: Setup Mock API Discovery
	// Create mock API resource lists and HTTP server to simulate Kubernetes API discovery.
	// This allows tests to query available resources without connecting to a real cluster.
	resourceLists, apiGroupList := setupAPIResourceLists()
	server = createMockHTTPServer(resourceLists, apiGroupList)

	// Create a fake discovery client that uses our mock server
	fakeDiscovery = discoveryclient.NewDiscoveryClientForConfigOrDie(
		&restclient.Config{Host: server.URL},
	)

	// Verify discovery client works correctly with mock data
	By("verifying discovery client")
	testResourceList := resourceLists["operator.open-cluster-management.io/v1"]
	got, err := fakeDiscovery.ServerResourcesForGroupVersion("operator.open-cluster-management.io/v1")
	Expect(err).NotTo(HaveOccurred())
	Expect(reflect.DeepEqual(got, testResourceList)).To(BeTrue())

	_, err = fakeDiscovery.ServerGroups()
	Expect(err).NotTo(HaveOccurred())

	// STEP 2: Setup Test Kubernetes Environment
	// testEnv creates a local etcd and kube-apiserver for testing.
	// This provides a real Kubernetes API that controllers can interact with.
	By("starting test environments")
	_, err = setupTestEnvironments()
	Expect(err).NotTo(HaveOccurred())

	// STEP 3: Register Custom Resource Schemes
	// Add all the custom resource definitions that our controllers need to work with.
	// This includes backup schedules, restores, managed clusters, etc.
	By("registering schemes")
	err = setupSchemes()
	Expect(err).NotTo(HaveOccurred())

	// STEP 4: Create Test Objects and Dynamic Client
	// Pre-populate the test environment with mock Kubernetes objects that tests can use.
	// The dynamic client allows runtime manipulation of unstructured objects.
	By("creating test objects and dynamic client")
	testObjects, gvks := createTestObjects()
	gvrMappings := setupGVRMappings()

	unstructuredScheme := runtime.NewScheme()
	for _, gvk := range gvks {
		unstructuredScheme.AddKnownTypes(gvk.GroupVersion())
	}

	// Create a fake dynamic client with only the InternalHubComponent (mch) object
	// This is the only preloaded object actually used by getInternalHubResource()
	// Other objects are created dynamically by individual tests as needed
	dynR := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		unstructuredScheme,
		gvrMappings,
		testObjects["mch"], // Only include the InternalHubComponent - actually used by restore controller
	)

	// STEP 5: Setup Controller Manager and Reconcilers
	// Create the controller manager using testEnv's configuration.
	// This connects our controllers to the test Kubernetes API server.
	By("setting up controllers")
	mgr, err := ctrl.NewManager(testEnv.Config, ctrl.Options{Scheme: scheme.Scheme})
	Expect(err).ToNot(HaveOccurred())

	// Register our backup/restore controllers with the manager
	// Each controller uses the mock discovery and dynamic clients for testing
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

	// STEP 6: Start Controller Manager
	// Start the controller manager in a separate goroutine to handle reconciliation.
	// Use a cancellable context for graceful shutdown during test cleanup.
	var ctx context.Context
	ctx, cancel = context.WithCancel(context.Background())
	managerDone = make(chan struct{})

	go func() {
		defer GinkgoRecover()
		defer close(managerDone) // Signal when manager goroutine exits
		err = mgr.Start(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			Expect(err).ToNot(HaveOccurred())
		}
	}()

	// Set resources to backup for tests
	resourcesToBackup = []string{"placement.cluster.open-cluster-management.io"}
	includedActivationAPIGroupsByName = []string{"other.hive.openshift.io"}
})

var _ = AfterSuite(func() {
	By("tearing down the test environment")

	// STEP 1: Gracefully stop the controller manager
	// Cancel the context to signal controllers to stop processing
	if cancel != nil {
		By("stopping controller manager")
		cancel()

		// Wait for the manager goroutine to actually finish
		// This ensures all controller goroutines are properly cleaned up
		By("waiting for controller manager to stop")
		Eventually(func() bool {
			select {
			case <-managerDone:
				return true
			default:
				return false
			}
		}, time.Second*10, time.Millisecond*100).Should(BeTrue())
	}

	// STEP 2: Stop the test Kubernetes environment
	// Clean up the etcd and kube-apiserver processes
	// Use Eventually to handle any cleanup delays gracefully
	Eventually(func() bool {
		err := testEnv.Stop()
		return err == nil
	}, time.Minute*1, time.Millisecond*250).Should(BeTrue())
})

// setupAPIResourceLists creates all the API resource lists needed for testing
func setupAPIResourceLists() (map[string]*metav1.APIResourceList, *metav1.APIGroupList) {
	resourceLists := make(map[string]*metav1.APIResourceList)

	setupArgoProjAPIResources(resourceLists)
	setupOpenshiftAPIResources(resourceLists)
	setupAppsAPIResources(resourceLists)
	setupAddonAPIResources(resourceLists)
	setupClusterAPIResources(resourceLists)
	setupProxyAPIResources(resourceLists)
	setupHiveAPIResources(resourceLists)
	setupAuthAPIResources(resourceLists)
	setupOperatorAPIResources(resourceLists)

	apiGroupList := createAPIGroupList()
	return resourceLists, apiGroupList
}

// setupArgoProjAPIResources configures ArgoProj API resources
func setupArgoProjAPIResources(resourceLists map[string]*metav1.APIResourceList) {
	resourceLists["argoproj.io/v1alpha1"] = &metav1.APIResourceList{
		GroupVersion: "argoproj.io/v1alpha1",
		APIResources: []metav1.APIResource{
			{Name: "applications", Namespaced: true, Kind: "Application"},
			{Name: "applicationsets", Namespaced: true, Kind: "ApplicationSet"},
			{Name: "argocds", Namespaced: true, Kind: "Argocd"},
		},
	}
}

// setupOpenshiftAPIResources configures OpenShift API resources
func setupOpenshiftAPIResources(resourceLists map[string]*metav1.APIResourceList) {
	resourceLists["config.openshift.io/v1"] = &metav1.APIResourceList{
		GroupVersion: "config.openshift.io/v1",
		APIResources: []metav1.APIResource{
			{Name: "clusterversions", Namespaced: false, Kind: "ClusterVersion"},
		},
	}
}

// setupAppsAPIResources configures Apps API resources
func setupAppsAPIResources(resourceLists map[string]*metav1.APIResourceList) {
	appsBeta1Resources := []metav1.APIResource{
		{Name: "channels", Namespaced: true, Kind: "Channel"},
		{Name: "subscriptions", Namespaced: true, Kind: "Subscription"},
	}

	resourceLists["apps.open-cluster-management.io/v1beta1"] = &metav1.APIResourceList{
		GroupVersion: "apps.open-cluster-management.io/v1beta1",
		APIResources: appsBeta1Resources,
	}

	resourceLists["apps.open-cluster-management.io/v1"] = &metav1.APIResourceList{
		GroupVersion: "apps.open-cluster-management.io/v1",
		APIResources: appsBeta1Resources,
	}
}

// setupAddonAPIResources configures Addon API resources
func setupAddonAPIResources(resourceLists map[string]*metav1.APIResourceList) {
	resourceLists["addon.open-cluster-management.io/v1alpha1"] = &metav1.APIResourceList{
		GroupVersion: "addon.open-cluster-management.io/v1alpha1",
		APIResources: []metav1.APIResource{
			{Name: "managedclusteraddons", Namespaced: true, Kind: "ManagedClusterAddOn"},
			{Name: "clustermanagementaddons", Namespaced: false, Kind: "ClusterManagementAddOn"},
		},
	}
}

// setupClusterAPIResources configures Cluster API resources
func setupClusterAPIResources(resourceLists map[string]*metav1.APIResourceList) {
	clusterResources := []metav1.APIResource{
		{Name: "placements", Namespaced: true, Kind: "Placement"},
		{Name: "clustercurators", Namespaced: true, Kind: "ClusterCurator"},
		{Name: "managedclustersets", Namespaced: false, Kind: "ManagedClusterSet"},
		{Name: "backupschedules", Namespaced: true, Kind: "BackupSchedule"},
		{Name: "managedclusters", Namespaced: true, Kind: "ManagedCluster"},
	}

	resourceLists["cluster.open-cluster-management.io/v1beta1"] = &metav1.APIResourceList{
		GroupVersion: "cluster.open-cluster-management.io/v1beta1",
		APIResources: clusterResources,
	}

	resourceLists["cluster.open-cluster-management.io/v1"] = &metav1.APIResourceList{
		GroupVersion: "cluster.open-cluster-management.io/v1",
		APIResources: clusterResources,
	}
}

// setupProxyAPIResources configures Proxy API resources
func setupProxyAPIResources(resourceLists map[string]*metav1.APIResourceList) {
	resourceLists["proxy.open-cluster-management.io/v1beta1"] = &metav1.APIResourceList{
		GroupVersion: "proxy.open-cluster-management.io/v1beta1",
		APIResources: []metav1.APIResource{
			{Name: "managedclustermutators", Namespaced: false, Kind: "AdmissionReview"},
		},
	}
}

// setupHiveAPIResources configures Hive API resources
func setupHiveAPIResources(resourceLists map[string]*metav1.APIResourceList) {
	resourceLists["hive.openshift.io/v1"] = &metav1.APIResourceList{
		GroupVersion: "hive.openshift.io/v1",
		APIResources: []metav1.APIResource{
			{Name: "clusterdeployments", Namespaced: false, Kind: "ClusterDeployment"},
			{Name: "dnszones", Namespaced: false, Kind: "DNSZone"},
			{Name: "clusterimageset", Namespaced: false, Kind: "ClusterImageSet"},
			{Name: "hiveconfig", Namespaced: false, Kind: "HiveConfig"},
		},
	}

	resourceLists["other.hive.openshift.io/v1"] = &metav1.APIResourceList{
		GroupVersion: "other.hive.openshift.io/v1",
		APIResources: []metav1.APIResource{
			{Name: "clusterpools", Namespaced: true, Kind: "ClusterPool"},
		},
	}
}

// setupAuthAPIResources configures Authentication API resources
func setupAuthAPIResources(resourceLists map[string]*metav1.APIResourceList) {
	resourceLists["authentication.open-cluster-management.io/v1beta1"] = &metav1.APIResourceList{
		GroupVersion: "authentication.open-cluster-management.io/v1beta1",
		APIResources: []metav1.APIResource{
			{Name: "managedserviceaccounts", Namespaced: true, Kind: "ManagedServiceAccount"},
		},
	}
}

// setupOperatorAPIResources configures Operator API resources
func setupOperatorAPIResources(resourceLists map[string]*metav1.APIResourceList) {
	resourceLists["operator.open-cluster-management.io/v1"] = &metav1.APIResourceList{
		GroupVersion: "operator.open-cluster-management.io/v1",
		APIResources: []metav1.APIResource{
			{Name: "internalhubcomponents", Namespaced: true, Kind: "InternalHubComponent"},
		},
	}
}

// createAPIGroupList creates the API group list for discovery
func createAPIGroupList() *metav1.APIGroupList {
	return &metav1.APIGroupList{
		Groups: []metav1.APIGroup{
			createConfigAPIGroup(),
			createArgoProjAPIGroup(),
			createClusterAPIGroup(),
			createProxyAPIGroup(),
			createHiveAPIGroups(),
			createOtherHiveAPIGroup(),
			createAppsAPIGroup(),
			createAuthAPIGroup(),
			createOperatorAPIGroup(),
			createAddonAPIGroup(),
		},
	}
}

// createConfigAPIGroup creates the config.openshift.io API group
func createConfigAPIGroup() metav1.APIGroup {
	return metav1.APIGroup{
		Name: "config.openshift.io",
		Versions: []metav1.GroupVersionForDiscovery{
			{GroupVersion: "config.openshift.io/v1", Version: "v1"},
		},
	}
}

// createArgoProjAPIGroup creates the argoproj.io API group
func createArgoProjAPIGroup() metav1.APIGroup {
	return metav1.APIGroup{
		Name: "argoproj.io",
		Versions: []metav1.GroupVersionForDiscovery{
			{GroupVersion: "argoproj.io/v1", Version: "v1"},
			{GroupVersion: "argoproj.io/v1beta1", Version: "v1beta1"},
		},
	}
}

// createClusterAPIGroup creates the cluster.open-cluster-management.io API group
func createClusterAPIGroup() metav1.APIGroup {
	return metav1.APIGroup{
		Name: "cluster.open-cluster-management.io",
		Versions: []metav1.GroupVersionForDiscovery{
			{GroupVersion: "cluster.open-cluster-management.io/v1beta1", Version: "v1beta1"},
			{GroupVersion: "cluster.open-cluster-management.io/v1", Version: "v1"},
		},
	}
}

// createProxyAPIGroup creates the proxy.open-cluster-management.io API group
func createProxyAPIGroup() metav1.APIGroup {
	return metav1.APIGroup{
		Name: "proxy.open-cluster-management.io",
		Versions: []metav1.GroupVersionForDiscovery{
			{GroupVersion: "proxy.open-cluster-management.io/v1beta1", Version: "v1beta1"},
			{GroupVersion: "proxy.open-cluster-management.io/v1", Version: "v1"},
		},
	}
}

// createHiveAPIGroups creates the hive.openshift.io API group
func createHiveAPIGroups() metav1.APIGroup {
	return metav1.APIGroup{
		Name: "hive.openshift.io",
		Versions: []metav1.GroupVersionForDiscovery{
			{GroupVersion: "hive.openshift.io/v1", Version: "v1"},
			{GroupVersion: "hive.openshift.io/v1beta1", Version: "v1beta1"},
		},
	}
}

// createOtherHiveAPIGroup creates the other.hive.openshift.io API group
func createOtherHiveAPIGroup() metav1.APIGroup {
	return metav1.APIGroup{
		Name: "other.hive.openshift.io",
		Versions: []metav1.GroupVersionForDiscovery{
			{GroupVersion: "other.hive.openshift.io/v1", Version: "v1"},
		},
	}
}

// createAppsAPIGroup creates the apps.open-cluster-management.io API group
func createAppsAPIGroup() metav1.APIGroup {
	return metav1.APIGroup{
		Name: "apps.open-cluster-management.io",
		Versions: []metav1.GroupVersionForDiscovery{
			{GroupVersion: "apps.open-cluster-management.io/v1beta1", Version: "v1beta1"},
			{GroupVersion: "apps.open-cluster-management.io/v1", Version: "v1"},
		},
	}
}

// createAuthAPIGroup creates the authentication.open-cluster-management.io API group
func createAuthAPIGroup() metav1.APIGroup {
	return metav1.APIGroup{
		Name: "authentication.open-cluster-management.io",
		Versions: []metav1.GroupVersionForDiscovery{
			{GroupVersion: "authentication.open-cluster-management.io/v1beta1", Version: "v1beta1"},
		},
	}
}

// createOperatorAPIGroup creates the operator.open-cluster-management.io API group
func createOperatorAPIGroup() metav1.APIGroup {
	return metav1.APIGroup{
		Name: "operator.open-cluster-management.io",
		Versions: []metav1.GroupVersionForDiscovery{
			{GroupVersion: "operator.open-cluster-management.io/v1", Version: "v1"},
		},
	}
}

// createAddonAPIGroup creates the addon.open-cluster-management.io API group
func createAddonAPIGroup() metav1.APIGroup {
	return metav1.APIGroup{
		Name: "addon.open-cluster-management.io",
		Versions: []metav1.GroupVersionForDiscovery{
			{GroupVersion: "addon.open-cluster-management.io/v1alpha1", Version: "v1alpha1"},
		},
	}
}

// createMockHTTPServer creates the mock HTTP server for API discovery
func createMockHTTPServer(
	resourceLists map[string]*metav1.APIResourceList,
	apiGroupList *metav1.APIGroupList,
) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var list interface{}

		switch req.URL.Path {
		case "/api":
			list = &metav1.APIVersions{Versions: []string{"v1", "v1beta1"}}
		case "/apis":
			list = apiGroupList
		default:
			// Handle specific API group version requests
			if resourceList, exists := resourceLists[req.URL.Path[6:]]; exists { // Remove "/apis/" prefix
				list = resourceList
			} else {
				w.WriteHeader(http.StatusNotFound)
				return
			}
		}

		output, err := json.Marshal(list)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(output)
	}))
}

// createTestObjects creates only the test objects that are actually used by the controllers
// This reduces complexity by only including objects that provide real test coverage
func createTestObjects() (map[string]*unstructured.Unstructured, []schema.GroupVersionKind) {
	objects := make(map[string]*unstructured.Unstructured)
	var gvks []schema.GroupVersionKind

	// Only create the InternalHubComponent - it's actually used by getInternalHubResource()
	createMCHObject(objects, &gvks)

	return objects, gvks
}

// createMCHObject creates the MCH test object
func createMCHObject(objects map[string]*unstructured.Unstructured, gvks *[]schema.GroupVersionKind) {
	objects["mch"] = &unstructured.Unstructured{}
	objects["mch"].SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "operator.open-cluster-management.io/v1",
		"kind":       "InternalHubComponent",
		"metadata": map[string]interface{}{
			"name":      testMCHName,
			"namespace": testNamespace,
			"spec":      map[string]interface{}{},
		},
	})
	*gvks = append(*gvks, schema.GroupVersionKind{
		Group: "operator.open-cluster-management.io", Version: "v1", Kind: "InternalHubComponent",
	})
}

// setupGVRMappings creates all the GVR to ListKind mappings
func setupGVRMappings() map[schema.GroupVersionResource]string {
	return map[schema.GroupVersionResource]string{
		{Group: "operator.open-cluster-management.io", Version: "v1",
			Resource: "internalhubcomponents"}: "InternalHubComponentList",
		{Group: "authentication.open-cluster-management.io", Version: "v1beta1",
			Resource: "managedserviceaccounts"}: "ManagedServiceAccountList",
		{Group: "config.openshift.io", Version: "v1",
			Resource: "clusterversions"}: "ClusterVersionList",
		{Group: "hive.openshift.io", Version: "v1",
			Resource: "clusterdeployments"}: "ClusterDeploymentList",
		{Group: "cluster.open-cluster-management.io", Version: "v1beta1",
			Resource: "placements"}: "PlacementList",
		{Group: "cluster.open-cluster-management.io", Version: "v1beta1",
			Resource: "clustercurators"}: "ClusterCuratorList",
		{Group: "apps.open-cluster-management.io", Version: "v1beta1",
			Resource: "channels"}: "ChannelList",
		{Group: "cluster.open-cluster-management.io", Version: "v1beta1",
			Resource: "managedclusters"}: "ManagedClusterList",
		{Group: "cluster.open-cluster-management.io", Version: "v1beta1",
			Resource: "managedclustersets"}: "ManagedClusterSetList",
		{Group: "cluster.open-cluster-management.io", Version: "v1beta1",
			Resource: "backupschedules"}: "BackupScheduleList",
		{Group: "proxy.open-cluster-management.io", Version: "v1beta1",
			Resource: "managedclustermutators"}: "AdmissionReviewList",
		{Group: "other.hive.openshift.io", Version: "v1",
			Resource: "clusterpools"}: "ClusterPoolList",
		{Group: "hive.openshift.io", Version: "v1",
			Resource: "dnszones"}: "DNSZoneList",
		{Group: "hive.openshift.io", Version: "v1",
			Resource: "clusterimageset"}: "ClusterImageSetList",
		{Group: "hive.openshift.io", Version: "v1",
			Resource: "hiveconfig"}: "HiveConfigList",
		{Group: "apps.open-cluster-management.io", Version: "v1beta1",
			Resource: "subscriptions"}: "SubscriptionList",
		{Group: "addon.open-cluster-management.io", Version: "v1alpha1",
			Resource: "managedclusteraddons"}: "ManagedClusterAddOnList",
		{Group: "addon.open-cluster-management.io", Version: "v1alpha1",
			Resource: "clustermanagementaddons"}: "ClusterManagementAddOnList",
	}
}

// setupSchemes registers all required schemes
func setupSchemes() error {
	schemes := []func(*runtime.Scheme) error{
		ocinfrav1.AddToScheme,
		backupv1beta1.AddToScheme,
		clusterv1.AddToScheme,
		chnv1.AddToScheme,
		addonv1alpha1.AddToScheme,
		workv1.AddToScheme,
		hivev1.AddToScheme,
		certsv1.AddToScheme,
		operatorapiv1.AddToScheme,
		rbacv1.AddToScheme,
		veleroapi.AddToScheme,
	}

	for _, addToScheme := range schemes {
		if err := addToScheme(scheme.Scheme); err != nil {
			return err
		}
	}
	return nil
}

// setupTestEnvironments creates and configures the test Kubernetes environment
// This function:
// 1. Creates a testEnv with paths to CRD files
// 2. Starts a local etcd and kube-apiserver
// 3. Creates a Kubernetes client connected to the test server
// 4. Returns the environment for use by controllers
func setupTestEnvironments() (*envtest.Environment, error) {
	// Configure testEnv with CRD paths so custom resources are available
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "config", "crd", "bases"), // Our operator's CRDs
			filepath.Join("..", "hack", "crds"),           // Third-party CRDs (Velero, etc.)
		},
		ErrorIfCRDPathMissing: true, // Fail fast if CRDs are missing
	}

	// Start the test environment (etcd + kube-apiserver)
	cfg, err := testEnv.Start()
	if err != nil {
		return nil, err
	}

	// Create a Kubernetes client connected to our test API server
	// This client will be used by individual tests to interact with resources
	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	if err != nil {
		return nil, err
	}

	return testEnv, nil
}
