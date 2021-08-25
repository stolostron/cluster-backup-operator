package controllers

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	v1beta1 "github.com/open-cluster-management/cluster-backup-operator/api/v1beta1"
	ocinfrav1 "github.com/openshift/api/config/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	veleroapi "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
)

func setVeleroPhase(restore *v1beta1.Restore, phase veleroapi.RestorePhase) {
	restore.Status.VeleroRestore.Status.Phase = phase
}

func initSecret(name, namespace string) corev1.Secret {
	return corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
}

var _ = Describe("For utility functions of Restore", func() {
	var (
		restoreName string = "the-restore-name"
	)
	Context("When a restore is available", func() {
		It("isRestoreFinished should return value based on the status", func() {
			rhacmRestore := v1beta1.Restore{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "cluster.open-cluster-management.io/v1beta1",
					Kind:       "Restore",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      restoreName,
					Namespace: "",
				},
				Status: v1beta1.RestoreStatus{
					VeleroRestore: &veleroapi.Restore{
						Status: veleroapi.RestoreStatus{
							Phase: veleroapi.RestorePhaseInProgress,
						},
					},
				},
			}

			Expect(isRestoreFinished(&rhacmRestore)).Should(BeFalse())

			setVeleroPhase(&rhacmRestore, veleroapi.RestorePhaseCompleted)
			Expect(isRestoreFinished(&rhacmRestore)).Should(BeTrue())

			setVeleroPhase(&rhacmRestore, veleroapi.RestorePhaseFailed)
			Expect(isRestoreFinished(&rhacmRestore)).Should(BeTrue())

			setVeleroPhase(&rhacmRestore, veleroapi.RestorePhasePartiallyFailed)
			Expect(isRestoreFinished(&rhacmRestore)).Should(BeTrue())

			setVeleroPhase(&rhacmRestore, veleroapi.RestorePhaseFailedValidation)
			Expect(isRestoreFinished(&rhacmRestore)).Should(BeTrue())

			Expect(isRestoreFinished(nil)).Should(BeFalse())

			rhacmRestore.Status.VeleroRestore = nil
			Expect(isRestoreFinished(&rhacmRestore)).Should(BeFalse())

		})
	})
})

var _ = Describe("For utility restore functions: to test secret filetering", func() {
	var (
		ctx                    context.Context
		secrets                corev1.SecretList
		adminKubeConfigSecrets []corev1.Secret
		bootstrapSASecrets     []corev1.Secret
	)
	BeforeEach(func() { // default values
		ctx = context.Background()
		secrets = corev1.SecretList{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "v1",
				Kind:       "List",
			},
			Items: []corev1.Secret{},
		}
	})

	JustBeforeEach(func() {
		filterSecrets(ctx, &secrets, &adminKubeConfigSecrets, &bootstrapSASecrets)
	})

	Context("when secret available", func() {
		BeforeEach(func() { // default values
			ns := "remote-managed-one"
			secrets.Items = append(secrets.Items, initSecret("a-base64-encoded-secret", ns))
			secrets.Items = append(secrets.Items, initSecret("remote-managed-one-0-td8kk-admin-kubeconfig", ns))
			secrets.Items = append(secrets.Items, initSecret("remote-managed-one-bootstrap-sa-token-xkxdv", ns))
			secrets.Items = append(secrets.Items, initSecret("the-secret-thing", ns))
		})
		It("should return them ", func() {
			Expect(len(adminKubeConfigSecrets)).Should(BeNumerically("==", 1))
			Expect(len(bootstrapSASecrets)).Should(BeNumerically("==", 1))
		})
		It("should return the right ones", func() {
			Expect(adminKubeConfigSecrets[0].Name).Should(BeIdenticalTo("remote-managed-one-0-td8kk-admin-kubeconfig"))
			Expect(bootstrapSASecrets[0].Name).Should(BeIdenticalTo("remote-managed-one-bootstrap-sa-token-xkxdv"))
		})
	})

})

const (
	managedClusterAdminKubeconfig string = `
clusters:
- cluster:
    certificate-authority-data: LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSURCakNDQWU2Z0F3SUJBZ0lCQVRBTkJna3Foa2lHOXcwQkFRc0ZBREFWTVJNd0VRWURWUVFERXdwdGFXNXAKYTNWaVpVTkJNQjRYRFRJeE1EZ3lNakEzTlRBd01sb1hEVE14TURneU1UQTNOVEF3TWxvd0ZURVRNQkVHQTFVRQpBeE1LYldsdWFXdDFZbVZEUVRDQ0FTSXdEUVlKS29aSWh2Y05BUUVCQlFBRGdnRVBBRENDQVFvQ2dnRUJBTC9rCmJUSS8reE9ickltek1hR0ZGNzkzVlI0VTEzaWMyY2t0RGtUb3B0RFVESHp0SEd0RXpYTGJZeHhqS254eTNCSEsKOFdaSVFUc0lXSVM5dEhvUUQ3MWxDZFRaWHp1VDV4c0VhVEk2cStldmZZeFVNQktJQThUeE1wNkNtZTZ4T1cwVwprS0t5SG5DMXRNdGJlZnIzVWc4bERtL1pWOGUrR1NGVGNLcXFxMzF1VElUWU9NN3R0VVVWRnhYRXdXTkp1Z3lECm9qSzRhMEhaQ0lHMk42MmtNVFdNYzQvb0VNUmx0R1J1aFprNjgzbmIvRHl5MUJ2QUJVaHk2TmxkQnd0dUtweE4KSm5kejQ3elRXY1JtYXE3Y3BRbjZ6YjJRZWlnVkI1dHZVbHhXdnJsN1B2cURzRTVFK2V3NlpjVnRkL205bkRaKwpPRkxqRmxGN0k4ak1DZmc0RGQwQ0F3RUFBYU5oTUY4d0RnWURWUjBQQVFIL0JBUURBZ0trTUIwR0ExVWRKUVFXCk1CUUdDQ3NHQVFVRkJ3TUNCZ2dyQmdFRkJRY0RBVEFQQmdOVkhSTUJBZjhFQlRBREFRSC9NQjBHQTFVZERnUVcKQkJTUkQ0ZlB6YTdPQWdORWNQTHpVZy9mMWsvTUtEQU5CZ2txaGtpRzl3MEJBUXNGQUFPQ0FRRUFaUit5VEpVNgpjd1QyaG1hbS96QjZEUGM0VG5Yb2JDUHpCcXVxSTdHbDJqa2RvLzJFZ2tuWk1LbEtodnluL1B4a1V2Ni9nYXFMCjlhMW9uT3daU2Z6UXBNeldJR3prc1Y0TWYxY0k1d01mU3h5R3kzTS9aRHZpTmdRZ2FENGNXTVNwYVdEYXpXdmgKMFh3R1FlNEZJL0RNZVFhcElKVE1hNlcyWGJDSm05VEhOeldENndTVDFiam1EbUU0OFI2T0FQaDVRMEpuN3NBVQpJNCtpRlBQNC8zQldvSEQyMFlFT3BVQURiWkZhL1dTUyszTFdzUmFJT1c4aFBnczk3bHEvZHE4NDJUTVdHMm5sCjdTaXdXeHl5QUR3MWdZWGNETUIzRUVqY0ltOWpTYW80SENObDNkNWNpcnJnaGc0U001bE5WWWlSWGlIc3VhS3IKTWJsZUljZXRrbUJRa0E9PQotLS0tLUVORCBDRVJUSUZJQ0FURS0tLS0tCg==
    server: https://minikube.com:8443
  name: alfa-managed
contexts:
- context:
    cluster: alfa-managed
    user: admin
  name: admin
current-context: admin
preferences: {}
users:
- name: admin
  user:
    client-certificate-data: LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSURJVENDQWdtZ0F3SUJBZ0lCQWpBTkJna3Foa2lHOXcwQkFRc0ZBREFWTVJNd0VRWURWUVFERXdwdGFXNXAKYTNWaVpVTkJNQjRYRFRJeE1Ea3dNekl3TURVd00xb1hEVEl5TURrd05ESXdNRFV3TTFvd01URVhNQlVHQTFVRQpDaE1PYzNsemRHVnRPbTFoYzNSbGNuTXhGakFVQmdOVkJBTVREVzFwYm1scmRXSmxMWFZ6WlhJd2dnRWlNQTBHCkNTcUdTSWIzRFFFQkFRVUFBNElCRHdBd2dnRUtBb0lCQVFDKzNsRGY0K0lnbWQxd0k2ZE1tQ1VJbVZXS3hiL1YKbHgrdi9STTRhY1QvR0o1VUFqN2l2aWNoSzdPZDR1LzFYdllZbDVrTXpZalB3Si82RUFmWmwrTVhseTFsVEtIbQpvcXllMHpyd1ZENHhCdzloU0dkU2VWUnMrTmxRZmlENjF6NTRsTzhWOVBiMXh6Nzh0TGd1anY2QUtrczZwdFZkCkF0d3ZhZ2dIUmthUGNmeVd1U3UzelVnc1g1Y3hHSWZPYTgxQUQrVFE2UDA2MEk2dWRhbVViSnVhZG11UmUzWi8KYmRTNitxQTBkM3FnTFVDKzJwQXNDN09HOURCRG5rWFRvcVdGcHRHTmxaUTJBNzRHSnFQVEI3N3FFV3hESUpwWAowWUFiajVXM3I2SW53bWE0MzUxK052S1pNVEJoSGxsQjlIUFRVemlYNmpzMmdjNFdPOWY5VGlObEFnTUJBQUdqCllEQmVNQTRHQTFVZER3RUIvd1FFQXdJRm9EQWRCZ05WSFNVRUZqQVVCZ2dyQmdFRkJRY0RBUVlJS3dZQkJRVUgKQXdJd0RBWURWUjBUQVFIL0JBSXdBREFmQmdOVkhTTUVHREFXZ0JTUkQ0ZlB6YTdPQWdORWNQTHpVZy9mMWsvTQpLREFOQmdrcWhraUc5dzBCQVFzRkFBT0NBUUVBcks5Q0Vad21MZkJqQ0c5RUJtMnFBK0V3VWpQeklhRVpkb1VsCklsUjh5N1ZDcUhlOGpSaEdTZlRNNUN6KzA4WHlDanhZVDYxSVYycTJzNVNZZld3YVFESkMwaHByV0daMlpkNHcKZDF3eUhmTmI2MVRqN2hJT082VUdkSXFsWGpsdmo4NXAyY2R6UmoxM0Q3b2VnR21MK0xJb2FlRTJWREJKWTl3agpESzJLSWFYMk5TMm10c2ZmeThpYmVQMUM5azV5ZytveXFvSUhYcmFpZkNFRlFZejM3Mlc2bkxYUWMxYXpkZXdqClRoWXErSmVXaW9WazdSS2EzOXNvWDFvbmR2ZDVmUnpMVVY5SW9vUGErN0NkVnN1UCtJUEdXMXZRSWcvbmtkbkYKdXBGYmVjdVpPcE5uWjJ2TTdxbGhGNHlEbk9tZGUxRW5SWUhOQ2VSV2NUcXorL2ZHVlE9PQotLS0tLUVORCBDRVJUSUZJQ0FURS0tLS0tCg==
    client-key-data: LS0tLS1CRUdJTiBSU0EgUFJJVkFURSBLRVktLS0tLQpNSUlFb2dJQkFBS0NBUUVBdnQ1UTMrUGlJSm5kY0NPblRKZ2xDSmxWaXNXLzFaY2ZyLzBUT0duRS94aWVWQUkrCjRyNG5JU3V6bmVMdjlWNzJHSmVaRE0ySXo4Q2YraEFIMlpmakY1Y3RaVXloNXFLc250TTY4RlErTVFjUFlVaG4KVW5sVWJQalpVSDRnK3RjK2VKVHZGZlQyOWNjKy9MUzRMbzcrZ0NwTE9xYlZYUUxjTDJvSUIwWkdqM0g4bHJrcgp0ODFJTEYrWE1SaUh6bXZOUUEvazBPajlPdENPcm5XcGxHeWJtblpya1h0MmYyM1V1dnFnTkhkNm9DMUF2dHFRCkxBdXpodlF3UTU1RjA2S2xoYWJSalpXVU5nTytCaWFqMHdlKzZoRnNReUNhVjlHQUc0K1Z0NitpSjhKbXVOK2QKZmpieW1URXdZUjVaUWZSejAxTTRsK283Tm9IT0ZqdlgvVTRqWlFJREFRQUJBb0lCQUZpcFZySVozbEc4aDVOdQp6R2tWQjZidDYwR1NTR0ZFV1JEY0kxQ0NPV015SVdIdXhSMTRyUjZJZVdBdktiNDJSV1Q1RHJ4V3dXV1lHZmdECitGR0liNUhteE15WWcyQnFVbnRZcmJrenVNdjNkcHAvRXBmS0NvQ3dPK3BiSEtESTJaa1R2ZGZhT2RuRG15dXkKR3hodGppVWxBRnNYWW1kWlM4U3VvVm9YdC9FcmVGY3J2ckZQcUZ1bnQwVG1TWXlhOUhvVVRBaVFZNU9qWkZFNgpGWTM1V1ZQSnRGMGcwZWJRWDljOGVJVVdkVmFHbXdDNGE1S0tNUUtFSlB0R0gwdUxacDdVQlYyVFpsZ0dGaTg0CldoTnpDT1BsUjhOOTJxVVZ0U2pHdVNCY2l1VDFHNUtyTXYraFlwYWx3V2tVQ1poNVN6M0g0UEZhSGpETlJXYTQKYkUwTzkwRUNnWUVBeG9EZEE2L0JSY1NoRG5sS1E4REJpZ0xoZncvTW4xWGZpcHdVKzJaKzVhRTJLMVpWU2d2Ygo3Q01XY2VjQklnVjNHYkJ5WTF1SkU4dnJndmUyTFc4cDZSdmxPZnZmM2taT2hXS0J2clRJeW1PdERJWnZQVDRIClIyVE4rUkZ5TmJHRGlLRVgydHM2WS85WXpwMnNBRWpiVEN0RTFjQk5sVE1PR3RNV0NKZVFSREVDZ1lFQTlpZFIKRWU2RGhlditsbFh0Um1pcUxZamZ2TDFtR05yWnlrbUowZUEwR2ZMdmVLUWVmTWRma05yK2FUellrZVZrekVTdApFOFc1S1Q5K1lEQjk3TVRBS1hnMHgzSW05MFdlNG9Cc0MrMlc3L1dhRFhCR0MzdGlOd3Q0bk9KVnBmZko1RWFIClczdWpDaW96emtiUnRjd2swaUJVMVBwUTVPeE9XbnlrZ0psOVNYVUNnWUI3dzBxSml1SkkrcUNrSXBGZ0R1VmMKaEJGT0pHNmpCV3A3eEhiOGk5b2dsOVByVDBlY0JDclpYc01XdnoyZ2xhRzlYWnJrUWVVRWQ4YmVBRTRRbzllUQpwTGpWM3lta0wxZXpxRWhXdStiWThTNnF1WUxQdjBYUWlKUTNiMTR6QmZ1SmkwOFJRRkIybW5VblZYMHhMRHUyCmtOKzVHYzRGY1RDaEh1MEU3R0toY1FLQmdGMHZZU2R5cmVQREJXd1FOM1VTSm1wNmlJakJBcWVpSWhUTVpocEgKMERHS29GR0JmL0VvNE9yTG5NaG1PbTV3OHduSmJlUXdVL3BqaVFvTkVYN1N0UlI5NXkwaDc5Sm9Ucy9jWWdyWgo5T3YraEVWV0hZNDNOV1UxT0lIYnhTVEJlM0twcUpCZmE4ZHJWcFZlaGdGV3VSRzdINkpJNk5yaEFvQ0s4eE9rCkI2UUpBb0dBTThZOXAxaUtYS1BKTlpETmhzUnZRYWRISjRXdFlpQ29wZndZb0hVV0craEVCTk1nNFBsTjlIbE4KT3Q4di9SSDhKbW11R0VjaHR1N0lNN0did0o0dkp4ZTR4OWdJWmhta0ZBNGtSeEFROGtQcys3SEpjeTlYOHZ5Kwp6Wjg3TzUzSE0yczZwZ0t5ajdualRheHcrSGowU2Z2Njg1NnM0blB0cnNEdm5qeUs0VzA9Ci0tLS0tRU5EIFJTQSBQUklWQVRFIEtFWS0tLS0tCg==
`
)

func initManagedClusterBoostrapSATokenSecret(name, namespace string) corev1.Secret {
	return corev1.Secret{
		Type: corev1.SecretTypeServiceAccountToken,
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: map[string][]byte{
			"ca.crt":         []byte(`this_is_the_ca_certificate`),
			"service-ca.crt": []byte(`this_is_the_service_ca_certificate`),
			"token":          []byte(`this_is_the_token`),
			"namespace":      []byte(`this-is-the-namespace`),
		},
	}
}

func initKubeConfigSecret(name, namespace string) corev1.Secret {
	return corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: map[string][]byte{
			"kubeconfig": []byte(managedClusterAdminKubeconfig),
		},
	}
}

var _ = Describe("For utility restore functions: to test kubeconfig generation", func() {
	var (
		ctx          context.Context
		APIServerURL string
		Secret       corev1.Secret
	)

	BeforeEach(func() { // default values
		ctx = context.Background()
		APIServerURL = "https://api.dr-test-1.demo.red-chesterfield.com:6443"
		Secret = initManagedClusterBoostrapSATokenSecret("secret-name", "secret-namespace")
	})
	Context("With proper APIServer and Secret ", func() {
		It("should create a proper boostrap-hup-kubeconfig", func() {
			newKubeconfigSecret, err := newBootstrapHubKubeconfig(ctx, APIServerURL, Secret)
			Expect(err).NotTo(HaveOccurred())
			Expect(newKubeconfigSecret).NotTo(BeNil())
		})
		It("should create a client", func() {

		})
	})
})

var _ = Describe("For utility restore functions: to test client generation", func() {
	var (
		Secret corev1.Secret
	)
	BeforeEach(func() { // default values
		Secret = initKubeConfigSecret("secret-name", "secret-namespace")
	})
	Context("with proper kubeconfig secret", func() {
		It("should create kubeclient", func() {
			kubeclient, err := getKubeClientFromSecret(&Secret)
			Expect(err).NotTo((HaveOccurred()))
			Expect(kubeclient).NotTo(BeNil())
		})

	})

})

var _ = Describe("For utility restore function: getPublicURL", func() {
	var (
		ctx          context.Context
		infra        *ocinfrav1.Infrastructure
		APIServerURL string
	)

	const (
		apiName string = "https://api.the-incredible-cluster.demo.red-chesterfield.com:6443"
	)

	BeforeEach(func() {
		ctx = context.Background()
		infra = nil
	})

	JustBeforeEach(func() {
		if infra != nil {
			Expect(k8sClient.Create(ctx, infra)).Should(Succeed())
			k8sClient.Get(ctx, types.NamespacedName{Namespace: "", Name: "cluster"}, infra)

			if len(APIServerURL) > 0 {
				infra.Status.APIServerURL = APIServerURL
				Expect(k8sClient.Status().Update(ctx, infra)).NotTo(HaveOccurred())
			}
		}
	})

	Context("with client", func() {
		It("should return errorr and no public URL name", func() {
			publicURL, err := getPublicAPIServerURL(k8sClient)
			Expect(err).NotTo(BeNil())
			Expect(publicURL).To(BeEmpty())
		})
	})

	Context("with client and data", func() {
		BeforeEach(func() {
			infra = &ocinfrav1.Infrastructure{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: ocinfrav1.InfrastructureSpec{},
			}
			APIServerURL = apiName
		})
		It("should return the right value", func() {
			publicURL, err := getPublicAPIServerURL(k8sClient)
			Expect(err).NotTo(HaveOccurred())
			fmt.Println("-2222>", publicURL)
			Expect(publicURL == apiName).To(BeTrue())
		})
	})
})
