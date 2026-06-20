package controller

import (
	"context"
	"slices"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/yaml"

	headscalev1beta1 "github.com/infradohq/headscale-operator/api/v1beta1"
)

var _ = Describe("Headscale Controller", func() {
	Context("When reconciling a resource", func() {
		const (
			resourceName       = "test-headscale"
			namespace          = "default"
			readyConditionType = "Ready"
			timeout            = time.Second * 10
			interval           = time.Millisecond * 250
		)

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: namespace,
		}

		AfterEach(func() {
			By("Cleaning up all test resources with various suffixes")
			suffixes := []string{"", "-automanage", "-no-automanage", "-update", "-deletion", "-replicas", "-pvc", "-security", "-extras", "-bad-policy"}

			for _, suffix := range suffixes {
				crName := "test-headscale" + suffix
				resourceNSName := types.NamespacedName{
					Name:      crName,
					Namespace: namespace,
				}

				headscale := &headscalev1beta1.Headscale{}
				err := k8sClient.Get(ctx, resourceNSName, headscale)
				if err == nil {
					_ = k8sClient.Delete(ctx, headscale)
				}

				// envtest has no garbage collector, so clean up per-CR child resources explicitly.
				statefulSet := &appsv1.StatefulSet{}
				ssName := types.NamespacedName{Name: crName, Namespace: namespace}
				if err := k8sClient.Get(ctx, ssName, statefulSet); err == nil {
					_ = k8sClient.Delete(ctx, statefulSet)
				}
			}

			By("Waiting for the base StatefulSet to be cleaned up")
			statefulSet := &appsv1.StatefulSet{}
			baseSSName := types.NamespacedName{Name: "test-headscale", Namespace: namespace}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, baseSSName, statefulSet)
				return err != nil
			}, timeout, interval).Should(BeTrue())
		})

		It("should successfully create and reconcile a Headscale instance", func() {
			By("Creating the Headscale resource")
			headscale := &headscalev1beta1.Headscale{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: namespace,
				},
				Spec: headscalev1beta1.HeadscaleSpec{
					Version:  "v0.28.0",
					Replicas: 1,
					Config:   rawConfig(`{"server_url":"https://headscale.example.com","listen_addr":"0.0.0.0:8080","grpc_listen_addr":"0.0.0.0:50443","metrics_listen_addr":"0.0.0.0:9090"}`),
					PersistentVolumeClaim: headscalev1beta1.PersistentVolumeClaimConfig{
						Size: resource.NewQuantity(128*1024*1024, resource.BinarySI),
					},
					APIKey: headscalev1beta1.APIKeyConfig{
						SecretName: "test-api-key",
					},
				},
			}
			Expect(k8sClient.Create(ctx, headscale)).To(Succeed())

			By("Checking that the Headscale was created")
			createdHeadscale := &headscalev1beta1.Headscale{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, typeNamespacedName, createdHeadscale)
				return err == nil
			}, timeout, interval).Should(BeTrue())

			By("Reconciling the created resource")
			controllerReconciler := &HeadscaleReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Checking that the finalizer was added")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, typeNamespacedName, createdHeadscale)
				if err != nil {
					return false
				}
				return slices.Contains(createdHeadscale.Finalizers, headscaleFinalizer)
			}, timeout, interval).Should(BeTrue())

			By("Verifying ConfigMap was created")
			configMap := &corev1.ConfigMap{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      resourceName + "-config",
					Namespace: namespace,
				}, configMap)
				return err == nil
			}, timeout, interval).Should(BeTrue())

			By("Verifying StatefulSet was created")
			statefulSet := &appsv1.StatefulSet{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      resourceName,
					Namespace: namespace,
				}, statefulSet)
				return err == nil
			}, timeout, interval).Should(BeTrue())

			By("Verifying Service was created")
			service := &corev1.Service{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      resourceName,
					Namespace: namespace,
				}, service)
				return err == nil
			}, timeout, interval).Should(BeTrue())

			By("Verifying Metrics Service was created")
			metricsService := &corev1.Service{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      resourceName + "-metrics",
					Namespace: namespace,
				}, metricsService)
				return err == nil
			}, timeout, interval).Should(BeTrue())

			By("Verifying ServiceAccount was created")
			serviceAccount := &corev1.ServiceAccount{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      resourceName,
					Namespace: namespace,
				}, serviceAccount)
				return err == nil
			}, timeout, interval).Should(BeTrue())

			By("Verifying Role was created")
			role := &rbacv1.Role{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      resourceName,
					Namespace: namespace,
				}, role)
				return err == nil
			}, timeout, interval).Should(BeTrue())

			By("Verifying RoleBinding was created")
			roleBinding := &rbacv1.RoleBinding{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      resourceName,
					Namespace: namespace,
				}, roleBinding)
				return err == nil
			}, timeout, interval).Should(BeTrue())

			By("Verifying status conditions were set with ObservedGeneration")
			// envtest has no kubelet, so the StatefulSet never reports ready
			// replicas and the Ready condition stays False (NotReady). Assert the
			// status plumbing instead via ConfigValid, which the operator computes
			// itself, and confirm a Ready condition is present and observed.
			Eventually(func() bool {
				err := k8sClient.Get(ctx, typeNamespacedName, createdHeadscale)
				if err != nil {
					return false
				}
				configValid := false
				readyObserved := false
				for _, condition := range createdHeadscale.Status.Conditions {
					if condition.Type == "ConfigValid" &&
						condition.Status == metav1.ConditionTrue &&
						condition.ObservedGeneration == createdHeadscale.Generation {
						configValid = true
					}
					if condition.Type == readyConditionType &&
						condition.ObservedGeneration == createdHeadscale.Generation {
						readyObserved = true
					}
				}
				return configValid && readyObserved
			}, timeout, interval).Should(BeTrue())
		})

		It("should create StatefulSet with API key manager sidecar when AutoManage is true", func() {
			By("Creating the Headscale resource with AutoManage enabled")
			autoManage := true
			headscale := &headscalev1beta1.Headscale{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName + "-automanage",
					Namespace: namespace,
				},
				Spec: headscalev1beta1.HeadscaleSpec{
					Version:  "v0.28.0",
					Replicas: 1,
					Config:   rawConfig(`{"server_url":"https://headscale.example.com","listen_addr":"0.0.0.0:8080"}`),
					APIKey: headscalev1beta1.APIKeyConfig{
						AutoManage: &autoManage,
						SecretName: "test-api-key-automanage",
					},
				},
			}
			Expect(k8sClient.Create(ctx, headscale)).To(Succeed())

			autoManageNamespacedName := types.NamespacedName{
				Name:      resourceName + "-automanage",
				Namespace: namespace,
			}

			By("Reconciling the resource")
			controllerReconciler := &HeadscaleReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: autoManageNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying StatefulSet has apikey-manager container")
			statefulSet := &appsv1.StatefulSet{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      resourceName + "-automanage",
					Namespace: namespace,
				}, statefulSet)
				if err != nil {
					return false
				}
				for _, container := range statefulSet.Spec.Template.Spec.Containers {
					if container.Name == "apikey-manager" {
						return true
					}
				}
				return false
			}, timeout, interval).Should(BeTrue())

			By("Cleaning up the test resource")
			Expect(k8sClient.Delete(ctx, headscale)).To(Succeed())
		})

		It("should not create RBAC resources when AutoManage is false", func() {
			By("Creating the Headscale resource with AutoManage disabled")
			autoManage := false
			headscale := &headscalev1beta1.Headscale{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName + "-no-automanage",
					Namespace: namespace,
				},
				Spec: headscalev1beta1.HeadscaleSpec{
					Version:  "v0.28.0",
					Replicas: 1,
					Config:   rawConfig(`{"server_url":"https://headscale.example.com","listen_addr":"0.0.0.0:8080"}`),
					APIKey: headscalev1beta1.APIKeyConfig{
						AutoManage: &autoManage,
						SecretName: "test-api-key-no-automanage",
					},
				},
			}
			Expect(k8sClient.Create(ctx, headscale)).To(Succeed())

			noAutoManageNamespacedName := types.NamespacedName{
				Name:      resourceName + "-no-automanage",
				Namespace: namespace,
			}

			By("Reconciling the resource")
			controllerReconciler := &HeadscaleReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: noAutoManageNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying StatefulSet does not have apikey-manager container")
			statefulSet := &appsv1.StatefulSet{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      resourceName + "-no-automanage",
					Namespace: namespace,
				}, statefulSet)
				if err != nil {
					return false
				}
				for _, container := range statefulSet.Spec.Template.Spec.Containers {
					if container.Name == "apikey-manager" {
						return true
					}
				}
				return false
			}, timeout, interval).Should(BeFalse())

			By("Cleaning up the test resource")
			Expect(k8sClient.Delete(ctx, headscale)).To(Succeed())
		})

		It("should update ConfigMap when config changes", func() {
			By("Creating the Headscale resource")
			headscale := &headscalev1beta1.Headscale{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName + "-update",
					Namespace: namespace,
				},
				Spec: headscalev1beta1.HeadscaleSpec{
					Version:  "v0.28.0",
					Replicas: 1,
					Config:   rawConfig(`{"server_url":"https://headscale.example.com","listen_addr":"0.0.0.0:8080"}`),
				},
			}
			Expect(k8sClient.Create(ctx, headscale)).To(Succeed())

			updateNamespacedName := types.NamespacedName{
				Name:      resourceName + "-update",
				Namespace: namespace,
			}

			By("Reconciling the resource")
			controllerReconciler := &HeadscaleReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: updateNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Getting the initial ConfigMap")
			configMap := &corev1.ConfigMap{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      resourceName + "-update-config",
					Namespace: namespace,
				}, configMap)
				return err == nil
			}, timeout, interval).Should(BeTrue())

			initialData := configMap.Data["config.yaml"]

			By("Updating the Headscale config")
			Eventually(func() error {
				err := k8sClient.Get(ctx, updateNamespacedName, headscale)
				if err != nil {
					return err
				}
				headscale.Spec.Config = rawConfig(`{"server_url":"https://new-headscale.example.com","listen_addr":"0.0.0.0:8080"}`)
				return k8sClient.Update(ctx, headscale)
			}, timeout, interval).Should(Succeed())

			By("Reconciling again")
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: updateNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying ConfigMap was updated")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      resourceName + "-update-config",
					Namespace: namespace,
				}, configMap)
				if err != nil {
					return false
				}
				return configMap.Data["config.yaml"] != initialData
			}, timeout, interval).Should(BeTrue())

			By("Cleaning up the test resource")
			Expect(k8sClient.Delete(ctx, headscale)).To(Succeed())
		})

		It("should handle deletion with finalizer", func() {
			By("Creating the Headscale resource")
			headscale := &headscalev1beta1.Headscale{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName + "-deletion",
					Namespace: namespace,
				},
				Spec: headscalev1beta1.HeadscaleSpec{
					Version:  "v0.28.0",
					Replicas: 1,
					Config:   rawConfig(`{"server_url":"https://headscale.example.com","listen_addr":"0.0.0.0:8080"}`),
				},
			}
			Expect(k8sClient.Create(ctx, headscale)).To(Succeed())

			deletionNamespacedName := types.NamespacedName{
				Name:      resourceName + "-deletion",
				Namespace: namespace,
			}

			By("Reconciling to add finalizer")
			controllerReconciler := &HeadscaleReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: deletionNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying finalizer was added")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, deletionNamespacedName, headscale)
				if err != nil {
					return false
				}
				return slices.Contains(headscale.Finalizers, headscaleFinalizer)
			}, timeout, interval).Should(BeTrue())

			By("Deleting the Headscale")
			Expect(k8sClient.Delete(ctx, headscale)).To(Succeed())

			By("Reconciling the deletion")
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: deletionNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("should handle reconciliation when resource is not found", func() {
			By("Reconciling a non-existent resource")
			controllerReconciler := &HeadscaleReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			nonExistentName := types.NamespacedName{
				Name:      "non-existent-resource",
				Namespace: namespace,
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: nonExistentName,
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("should set correct replicas in StatefulSet", func() {
			By("Creating the Headscale resource with custom replicas")
			headscale := &headscalev1beta1.Headscale{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName + "-replicas",
					Namespace: namespace,
				},
				Spec: headscalev1beta1.HeadscaleSpec{
					Version:  "v0.28.0",
					Replicas: 3,
					Config:   rawConfig(`{"server_url":"https://headscale.example.com","listen_addr":"0.0.0.0:8080"}`),
				},
			}
			Expect(k8sClient.Create(ctx, headscale)).To(Succeed())

			replicasNamespacedName := types.NamespacedName{
				Name:      resourceName + "-replicas",
				Namespace: namespace,
			}

			By("Reconciling the resource")
			controllerReconciler := &HeadscaleReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: replicasNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying StatefulSet has correct replicas")
			statefulSet := &appsv1.StatefulSet{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      resourceName + "-replicas",
					Namespace: namespace,
				}, statefulSet)
				if err != nil {
					return false
				}
				return statefulSet.Spec.Replicas != nil && *statefulSet.Spec.Replicas == 3
			}, timeout, interval).Should(BeTrue())

			By("Cleaning up the test resource")
			Expect(k8sClient.Delete(ctx, headscale)).To(Succeed())
		})

		It("should set correct PVC size in StatefulSet", func() {
			By("Creating the Headscale resource with custom PVC size")
			customSize := resource.NewQuantity(256*1024*1024, resource.BinarySI)
			headscale := &headscalev1beta1.Headscale{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName + "-pvc",
					Namespace: namespace,
				},
				Spec: headscalev1beta1.HeadscaleSpec{
					Version:  "v0.28.0",
					Replicas: 1,
					Config:   rawConfig(`{"server_url":"https://headscale.example.com","listen_addr":"0.0.0.0:8080"}`),
					PersistentVolumeClaim: headscalev1beta1.PersistentVolumeClaimConfig{
						Size: customSize,
					},
				},
			}
			Expect(k8sClient.Create(ctx, headscale)).To(Succeed())

			pvcNamespacedName := types.NamespacedName{
				Name:      resourceName + "-pvc",
				Namespace: namespace,
			}

			By("Reconciling the resource")
			controllerReconciler := &HeadscaleReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: pvcNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying StatefulSet PVC template has correct size")
			statefulSet := &appsv1.StatefulSet{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      resourceName + "-pvc",
					Namespace: namespace,
				}, statefulSet)
				if err != nil {
					return false
				}
				if len(statefulSet.Spec.VolumeClaimTemplates) == 0 {
					return false
				}
				requestedStorage := statefulSet.Spec.VolumeClaimTemplates[0].Spec.Resources.Requests[corev1.ResourceStorage]
				return requestedStorage.Equal(*customSize)
			}, timeout, interval).Should(BeTrue())

			By("Cleaning up the test resource")
			Expect(k8sClient.Delete(ctx, headscale)).To(Succeed())
		})

		It("should set correct security context in StatefulSet", func() {
			By("Creating the Headscale resource")
			headscale := &headscalev1beta1.Headscale{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName + "-security",
					Namespace: namespace,
				},
				Spec: headscalev1beta1.HeadscaleSpec{
					Version:  "v0.28.0",
					Replicas: 1,
					Config:   rawConfig(`{"server_url":"https://headscale.example.com","listen_addr":"0.0.0.0:8080"}`),
				},
			}
			Expect(k8sClient.Create(ctx, headscale)).To(Succeed())

			securityNamespacedName := types.NamespacedName{
				Name:      resourceName + "-security",
				Namespace: namespace,
			}

			By("Reconciling the resource")
			controllerReconciler := &HeadscaleReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: securityNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying StatefulSet has correct security context")
			statefulSet := &appsv1.StatefulSet{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      resourceName + "-security",
					Namespace: namespace,
				}, statefulSet)
				if err != nil {
					return false
				}
				secCtx := statefulSet.Spec.Template.Spec.SecurityContext
				return secCtx != nil &&
					secCtx.RunAsUser != nil && *secCtx.RunAsUser == 65532 &&
					secCtx.RunAsGroup != nil && *secCtx.RunAsGroup == 65532 &&
					secCtx.FSGroup != nil && *secCtx.FSGroup == 65532 &&
					secCtx.RunAsNonRoot != nil && *secCtx.RunAsNonRoot == true
			}, timeout, interval).Should(BeTrue())

			By("Verifying pod-level seccompProfile is set to RuntimeDefault")
			podSecCtx := statefulSet.Spec.Template.Spec.SecurityContext
			Expect(podSecCtx.SeccompProfile).NotTo(BeNil(), "pod should set seccompProfile")
			Expect(podSecCtx.SeccompProfile.Type).To(Equal(corev1.SeccompProfileTypeRuntimeDefault), "pod should have RuntimeDefault seccomp profile")

			By("Verifying both headscale and apikey-manager containers are present")
			containerNames := make([]string, 0, len(statefulSet.Spec.Template.Spec.Containers))
			for _, c := range statefulSet.Spec.Template.Spec.Containers {
				containerNames = append(containerNames, c.Name)
			}
			Expect(containerNames).To(ContainElements("headscale", "apikey-manager"))

			By("Verifying all containers have restricted PodSecurity container security context")
			for _, container := range statefulSet.Spec.Template.Spec.Containers {
				csc := container.SecurityContext
				Expect(csc).NotTo(BeNil(), "container %s should have securityContext", container.Name)
				Expect(csc.AllowPrivilegeEscalation).NotTo(BeNil(), "container %s should set allowPrivilegeEscalation", container.Name)
				Expect(*csc.AllowPrivilegeEscalation).To(BeFalse(), "container %s should have allowPrivilegeEscalation=false", container.Name)
				Expect(csc.Capabilities).NotTo(BeNil(), "container %s should set capabilities", container.Name)
				Expect(csc.Capabilities.Drop).To(ContainElement(corev1.Capability("ALL")), "container %s should drop ALL capabilities", container.Name)
				Expect(csc.SeccompProfile).NotTo(BeNil(), "container %s should set seccompProfile", container.Name)
				Expect(csc.SeccompProfile.Type).To(Equal(corev1.SeccompProfileTypeRuntimeDefault), "container %s should have RuntimeDefault seccomp profile", container.Name)
			}

			By("Cleaning up the test resource")
			Expect(k8sClient.Delete(ctx, headscale)).To(Succeed())
		})

		It("should include extra env, volumes, and volume mounts in StatefulSet", func() {
			By("Creating the Headscale resource with extras")
			headscale := &headscalev1beta1.Headscale{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName + "-extras",
					Namespace: namespace,
				},
				Spec: headscalev1beta1.HeadscaleSpec{
					Version:  "v0.28.0",
					Replicas: 1,
					Config:   rawConfig(`{"server_url":"https://headscale.example.com","listen_addr":"0.0.0.0:8080"}`),
					ExtraEnv: []corev1.EnvVar{
						{
							Name:  "EXTRA_VAR",
							Value: "extra-value",
						},
					},
					ExtraVolumes: []corev1.Volume{
						{
							Name: "extra-vol",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: "extra-configmap",
									},
								},
							},
						},
					},
					ExtraVolumeMounts: []corev1.VolumeMount{
						{
							Name:      "extra-vol",
							MountPath: "/etc/extra",
							ReadOnly:  true,
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, headscale)).To(Succeed())

			extrasNamespacedName := types.NamespacedName{
				Name:      resourceName + "-extras",
				Namespace: namespace,
			}

			By("Reconciling the resource")
			controllerReconciler := &HeadscaleReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: extrasNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying StatefulSet has extra environment variable")
			statefulSet := &appsv1.StatefulSet{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      resourceName + "-extras",
					Namespace: namespace,
				}, statefulSet)
				if err != nil {
					return false
				}
				for _, env := range statefulSet.Spec.Template.Spec.Containers[0].Env {
					if env.Name == "EXTRA_VAR" && env.Value == "extra-value" {
						return true
					}
				}
				return false
			}, timeout, interval).Should(BeTrue())

			By("Verifying StatefulSet has extra volume")
			hasExtraVolume := false
			for _, vol := range statefulSet.Spec.Template.Spec.Volumes {
				if vol.Name == "extra-vol" {
					hasExtraVolume = true
					break
				}
			}
			Expect(hasExtraVolume).To(BeTrue())

			By("Verifying StatefulSet has extra volume mount")
			hasExtraMount := false
			for _, mount := range statefulSet.Spec.Template.Spec.Containers[0].VolumeMounts {
				if mount.Name == "extra-vol" && mount.MountPath == "/etc/extra" && mount.ReadOnly {
					hasExtraMount = true
					break
				}
			}
			Expect(hasExtraMount).To(BeTrue())

			By("Cleaning up the test resource")
			Expect(k8sClient.Delete(ctx, headscale)).To(Succeed())
		})
	})

	Context("Helper function tests", func() {
		It("should compute config hash correctly", func() {
			hashFor := func(jsonStr string) string {
				raw := rawConfig(jsonStr)
				rendered, err := renderConfigYAML(raw, parseConfigView(raw))
				Expect(err).NotTo(HaveOccurred())
				return computeConfigHash(rendered)
			}

			By("Testing equal configs produce equal hashes (independent of key order)")
			hash1 := hashFor(`{"server_url":"https://example.com","listen_addr":"0.0.0.0:8080"}`)
			hash2 := hashFor(`{"listen_addr":"0.0.0.0:8080","server_url":"https://example.com"}`)
			Expect(hash1).To(Equal(hash2))

			By("Testing different configs produce different hashes")
			hash3 := hashFor(`{"server_url":"https://different.com","listen_addr":"0.0.0.0:8080"}`)
			Expect(hash1).NotTo(Equal(hash3))
		})

		It("should extract port correctly", func() {
			By("Testing extractPort with address containing port")
			port := extractPort("0.0.0.0:9090", 8080)
			Expect(port).To(Equal(int32(9090)))

			By("Testing extractPort with colon-only prefix")
			port = extractPort(":50443", 8080)
			Expect(port).To(Equal(int32(50443)))

			By("Testing extractPort with no port")
			port = extractPort("0.0.0.0", 8080)
			Expect(port).To(Equal(int32(8080)))

			By("Testing extractPort with empty string")
			port = extractPort("", 8080)
			Expect(port).To(Equal(int32(8080)))

			By("Testing extractPort with invalid port")
			port = extractPort("0.0.0.0:invalid", 8080)
			Expect(port).To(Equal(int32(8080)))
		})

		It("should pass arbitrary/unknown config keys through to config.yaml verbatim", func() {
			By("Rendering a config containing keys the operator does not model")
			// randomize_client_port was removed in headscale 0.29 (issue #105);
			// the operator must no longer reason about it — it just passes through
			// whatever the user sets and lets headscale validate. A nested block
			// and an arbitrary future key must survive untouched too (issue #95).
			raw := rawConfig(`{
				"server_url": "https://headscale.example.com",
				"randomize_client_port": false,
				"derp": {"urls": [], "paths": ["/etc/derp/derp.yaml"]},
				"some_future_key": {"nested": "value"}
			}`)
			rendered, err := renderConfigYAML(raw, parseConfigView(raw))
			Expect(err).NotTo(HaveOccurred())

			var result map[string]any
			Expect(yaml.Unmarshal(rendered, &result)).To(Succeed())

			By("Verifying user-provided keys are preserved unchanged")
			Expect(result).To(HaveKeyWithValue("randomize_client_port", false))
			Expect(result).To(HaveKeyWithValue("server_url", "https://headscale.example.com"))
			derp, ok := result["derp"].(map[string]any)
			Expect(ok).To(BeTrue(), "expected derp key in config")
			Expect(derp).To(HaveKey("urls"))
			Expect(derp["urls"]).To(BeEmpty())
			Expect(derp["paths"]).To(ConsistOf("/etc/derp/derp.yaml"))
			Expect(result).To(HaveKey("some_future_key"))
		})

		It("should inject wiring defaults only when unset and preserve user values", func() {
			By("Rendering a config that omits the wiring keys")
			raw := rawConfig(`{"server_url":"https://headscale.example.com"}`)
			rendered, err := renderConfigYAML(raw, parseConfigView(raw))
			Expect(err).NotTo(HaveOccurred())
			var result map[string]any
			Expect(yaml.Unmarshal(rendered, &result)).To(Succeed())
			Expect(result).To(HaveKeyWithValue("listen_addr", defaultListenAddr))
			Expect(result).To(HaveKeyWithValue("metrics_listen_addr", defaultMetricsListenAddr))
			Expect(result).To(HaveKeyWithValue("grpc_listen_addr", defaultGRPCListenAddr))
			Expect(result).To(HaveKeyWithValue("unix_socket", defaultUnixSocket))

			By("Rendering a config that sets a wiring key explicitly")
			raw = rawConfig(`{"server_url":"https://headscale.example.com","listen_addr":"127.0.0.1:9999"}`)
			rendered, err = renderConfigYAML(raw, parseConfigView(raw))
			Expect(err).NotTo(HaveOccurred())
			Expect(yaml.Unmarshal(rendered, &result)).To(Succeed())
			Expect(result).To(HaveKeyWithValue("listen_addr", "127.0.0.1:9999"))
		})

		It("should pass headscale-only keys through untouched (no extra defaulting)", func() {
			By("Rendering a config with keys the operator does not consume")
			raw := rawConfig(`{"server_url":"https://h.example.com","database":{"type":"sqlite"}}`)
			rendered, err := renderConfigYAML(raw, parseConfigView(raw))
			Expect(err).NotTo(HaveOccurred())
			var result map[string]any
			Expect(yaml.Unmarshal(rendered, &result)).To(Succeed())

			By("Not inventing noise, prefixes, or a sqlite path the user didn't set")
			Expect(result).NotTo(HaveKey("noise"))
			Expect(result).NotTo(HaveKey("prefixes"))
			db, ok := result["database"].(map[string]any)
			Expect(ok).To(BeTrue())
			Expect(db).To(HaveKeyWithValue("type", "sqlite"))
			Expect(db).NotTo(HaveKey("sqlite"))
		})

		It("should parse the operator-relevant config view with defaults", func() {
			By("Defaulting every wiring key when the config is empty")
			view := parseConfigView(rawConfig(`{}`))
			Expect(view.ServerURL).To(BeEmpty())
			Expect(view.ListenAddr).To(Equal(defaultListenAddr))
			Expect(view.MetricsListenAddr).To(Equal(defaultMetricsListenAddr))
			Expect(view.GRPCListenAddr).To(Equal(defaultGRPCListenAddr))
			Expect(view.UnixSocket).To(Equal(defaultUnixSocket))
			Expect(view.PolicyMode).To(Equal(defaultPolicyMode))

			By("Reading user-set values, including nested policy.mode")
			view = parseConfigView(rawConfig(`{"server_url":"https://h.example.com","grpc_listen_addr":"0.0.0.0:7000","policy":{"mode":"database"}}`))
			Expect(view.ServerURL).To(Equal("https://h.example.com"))
			Expect(view.GRPCListenAddr).To(Equal("0.0.0.0:7000"))
			Expect(view.PolicyMode).To(Equal("database"))
		})

		It("should compute ConfigValid based on server_url presence", func() {
			h := &headscalev1beta1.Headscale{}
			missing := configValidCondition(h, parseConfigView(rawConfig(`{}`)), nil)
			Expect(missing.Type).To(Equal("ConfigValid"))
			Expect(missing.Status).To(Equal(metav1.ConditionFalse))
			Expect(missing.Reason).To(Equal("MissingServerURL"))

			ok := configValidCondition(h, parseConfigView(rawConfig(`{"server_url":"https://h.example.com"}`)), nil)
			Expect(ok.Status).To(Equal(metav1.ConditionTrue))
			Expect(ok.Reason).To(Equal("Valid"))
		})

		It("should generate correct labels", func() {
			By("Testing labelsForHeadscale")
			labels := labelsForHeadscale("test-instance")
			Expect(labels).To(HaveLen(3))
			Expect(labels["app.kubernetes.io/name"]).To(Equal("headscale"))
			Expect(labels["app.kubernetes.io/instance"]).To(Equal("test-instance"))
			Expect(labels["app.kubernetes.io/managed-by"]).To(Equal("headscale-operator"))
		})

		It("should surface a PolicyValid=False status condition when inline ACL policy is malformed", func() {
			ctx := context.Background()
			badPolicyName := types.NamespacedName{
				Name:      "test-headscale-bad-policy",
				Namespace: "default",
			}

			By("Creating the Headscale resource with an invalid inline policy")
			headscale := &headscalev1beta1.Headscale{
				ObjectMeta: metav1.ObjectMeta{
					Name:      badPolicyName.Name,
					Namespace: badPolicyName.Namespace,
				},
				Spec: headscalev1beta1.HeadscaleSpec{
					Version:  "v0.28.0",
					Replicas: 1,
					Config:   rawConfig(`{"server_url":"https://headscale.example.com","listen_addr":"0.0.0.0:8080"}`),
					ACLPolicy: headscalev1beta1.ACLPolicyConfig{
						// Missing quotes around object keys — the exact failure mode from issue #75.
						Inline: `{ acls: [ { action: "accept", src: ["*"], dst: ["*:*"] } ] }`,
					},
				},
			}
			Expect(k8sClient.Create(ctx, headscale)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, headscale)
			})

			By("Reconciling the resource")
			controllerReconciler := &HeadscaleReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: badPolicyName})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying PolicyValid condition is False with InvalidPolicy reason")
			updated := &headscalev1beta1.Headscale{}
			Eventually(func() bool {
				if err := k8sClient.Get(ctx, badPolicyName, updated); err != nil {
					return false
				}
				for _, c := range updated.Status.Conditions {
					if c.Type == "PolicyValid" &&
						c.Status == metav1.ConditionFalse &&
						c.Reason == "InvalidPolicy" {
						return true
					}
				}
				return false
			}, time.Second*10, time.Millisecond*250).Should(BeTrue())
		})
	})
})

var _ = Describe("validateACLPolicy", func() {
	It("returns PolicyValid=True when inline is empty", func() {
		h := &headscalev1beta1.Headscale{}
		cond := validateACLPolicy(h)
		Expect(cond.Type).To(Equal("PolicyValid"))
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal("Valid"))
	})

	It("returns PolicyValid=True for valid HuJSON", func() {
		h := &headscalev1beta1.Headscale{
			Spec: headscalev1beta1.HeadscaleSpec{
				ACLPolicy: headscalev1beta1.ACLPolicyConfig{
					Inline: `{
						// trailing-comma-friendly HuJSON is acceptable
						"acls": [{"action": "accept", "src": ["*"], "dst": ["*:*"]},],
					}`,
				},
			},
		}
		Expect(validateACLPolicy(h).Status).To(Equal(metav1.ConditionTrue))
	})

	It("returns PolicyValid=False with InvalidPolicy reason for malformed inline", func() {
		h := &headscalev1beta1.Headscale{
			Spec: headscalev1beta1.HeadscaleSpec{
				ACLPolicy: headscalev1beta1.ACLPolicyConfig{
					Inline: `{ acls: not-a-policy }`,
				},
			},
		}
		cond := validateACLPolicy(h)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal("InvalidPolicy"))
		Expect(cond.Message).NotTo(BeEmpty())
	})
})

var _ = Describe("computeReadyCondition", func() {
	bg := context.Background()

	newScheme := func() *runtime.Scheme {
		s := runtime.NewScheme()
		Expect(corev1.AddToScheme(s)).To(Succeed())
		Expect(appsv1.AddToScheme(s)).To(Succeed())
		return s
	}

	headscale := func() *headscalev1beta1.Headscale {
		return &headscalev1beta1.Headscale{
			ObjectMeta: metav1.ObjectMeta{Name: "hs", Namespace: "default", Generation: 1},
		}
	}

	statefulSet := func(ready, desired int32) *appsv1.StatefulSet {
		return &appsv1.StatefulSet{
			ObjectMeta: metav1.ObjectMeta{Name: "hs", Namespace: "default"},
			Spec:       appsv1.StatefulSetSpec{Replicas: ptr.To(desired)},
			Status:     appsv1.StatefulSetStatus{ReadyReplicas: ready},
		}
	}

	reconciler := func(objs ...client.Object) (*HeadscaleReconciler, *record.FakeRecorder) {
		rec := record.NewFakeRecorder(10)
		c := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(objs...).Build()
		return &HeadscaleReconciler{Client: c, Scheme: newScheme(), Recorder: rec}, rec
	}

	It("reports Ready=True when ready replicas meet desired", func() {
		r, _ := reconciler(statefulSet(1, 1))
		cond, requeue := r.computeReadyCondition(bg, headscale())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(requeue).To(BeFalse())
	})

	It("reports Ready=False StatefulSetMissing when the workload is absent", func() {
		r, _ := reconciler()
		cond, requeue := r.computeReadyCondition(bg, headscale())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal("StatefulSetMissing"))
		Expect(requeue).To(BeTrue())
	})

	It("surfaces a crashing headscale container's error and emits an event", func() {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "hs-0",
				Namespace: "default",
				Labels:    labelsForHeadscale("hs"),
			},
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{{
					Name:  "headscale",
					State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}},
					LastTerminationState: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							ExitCode: 1,
							Message:  "FATAL: The 'randomize_client_port' configuration key has been removed.",
						},
					},
				}},
			},
		}
		r, rec := reconciler(statefulSet(0, 1), pod)
		cond, requeue := r.computeReadyCondition(bg, headscale())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal("CrashLoopBackOff"))
		Expect(cond.Message).To(ContainSubstring("randomize_client_port"))
		Expect(requeue).To(BeTrue())
		Eventually(rec.Events).Should(Receive(ContainSubstring("randomize_client_port")))
	})
})
