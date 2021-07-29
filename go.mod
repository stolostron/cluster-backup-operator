module github.com/open-cluster-management-io/cluster-backup-operator

go 1.16

require (
	github.com/onsi/ginkgo v1.16.4
	github.com/onsi/gomega v1.10.2
	github.com/open-cluster-management/api v0.0.0-20210527013639-a6845f2ebcb1
	github.com/pkg/errors v0.9.1
	github.com/vmware-tanzu/velero v1.6.1
	k8s.io/api v0.20.2 // indirect
	k8s.io/apimachinery v0.20.2
	k8s.io/client-go v0.20.2
	sigs.k8s.io/controller-runtime v0.8.3
)
