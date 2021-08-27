module github.com/open-cluster-management-io/cluster-backup-operator

go 1.16

require (
	github.com/onsi/ginkgo v1.16.4
	github.com/onsi/gomega v1.10.2
	github.com/open-cluster-management/api v0.0.0-20210527013639-a6845f2ebcb1
	github.com/open-cluster-management/multicloud-operators-channel v1.2.2-2-20201130-37b47
	github.com/pkg/errors v0.9.1
	github.com/robfig/cron/v3 v3.0.0 // indirect
	github.com/stretchr/testify v1.7.0 // indirect
	github.com/vmware-tanzu/velero v1.6.1
	golang.org/x/crypto v0.0.0-20201221181555-eec23a3978ad // indirect
	golang.org/x/sys v0.0.0-20210124154548-22da62e12c0c // indirect
	k8s.io/api v0.20.2
	k8s.io/apiextensions-apiserver v0.20.2 // indirect
	k8s.io/apimachinery v0.20.2
	k8s.io/client-go v12.0.0+incompatible
	sigs.k8s.io/controller-runtime v0.8.3
)

replace (
	github.com/ulikunitz/xz => github.com/ulikunitz/xz v0.5.10
	k8s.io/client-go => k8s.io/client-go v0.20.2
)
