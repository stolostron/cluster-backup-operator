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
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	ocinfrav1 "github.com/openshift/api/config/v1"
	certsv1 "k8s.io/api/certificates/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/version"
	fakediscovery "k8s.io/client-go/discovery/fake"
	fakeclientset "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/envtest/printer"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	clusterv1 "github.com/open-cluster-management/api/cluster/v1"
	backupv1beta1 "github.com/open-cluster-management/cluster-backup-operator/api/v1beta1"
	chnv1 "github.com/open-cluster-management/multicloud-operators-channel/pkg/apis/apps/v1"
	hivev1 "github.com/openshift/hive/apis/hive/v1"
	operatorapiv1 "open-cluster-management.io/api/operator/v1"

	valeroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	//+kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var k8sClient client.Client
var testEnv *envtest.Environment

var managedClusterK8sClient client.Client
var testEnvManagedCluster *envtest.Environment

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecsWithDefaultAndCustomReporters(t,
		"Controller Suite",
		[]Reporter{printer.NewlineReporter{}})
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}

	fakeclient := fakeclientset.NewSimpleClientset()
	fakeDiscovery, ok := fakeclient.Discovery().(*fakediscovery.FakeDiscovery)
	// couldn't convert Discovery() to *FakeDiscovery
	Expect(ok).NotTo(BeFalse())

	testGitCommit := "v1.0.0"
	fakeDiscovery.FakedServerVersion = &version.Info{
		GitCommit: testGitCommit,
	}

	sv, err := fakeclient.Discovery().ServerVersion()
	Expect(err).ToNot(HaveOccurred())

	// unexpected faked discovery return value: %q", sv.GitCommit
	Expect(sv.GitCommit).To(BeIdenticalTo(testGitCommit))

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

	err = (&RestoreReconciler{
		KubeClient: nil,
		Client:     mgr.GetClient(),
		Scheme:     mgr.GetScheme(),
		Recorder:   mgr.GetEventRecorderFor("restore reconciler"),
	}).SetupWithManager(mgr)
	Expect(err).ToNot(HaveOccurred())

	err = (&BackupScheduleReconciler{
		Client:          mgr.GetClient(),
		DiscoveryClient: fakeDiscovery,
		Scheme:          mgr.GetScheme(),
	}).SetupWithManager(mgr)
	Expect(err).ToNot(HaveOccurred())

	go func() {
		err = mgr.Start(ctrl.SetupSignalHandler())
		Expect(err).ToNot(HaveOccurred())
	}()

}, 120)

var _ = AfterSuite(func() {
	By("tearing down the test environment")

	err := testEnvManagedCluster.Stop()
	Expect(err).NotTo(HaveOccurred())
	err = testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})
