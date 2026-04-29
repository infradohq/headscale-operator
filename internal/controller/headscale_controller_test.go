/*
Copyright 2025.

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
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/yaml"

	headscalev1beta1 "github.com/infradohq/headscale-operator/api/v1beta1"
)

var _ = Describe("Headscale Controller", func() {
	Context("When reconciling a Headscale resource", func() {
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
					Config: headscalev1beta1.HeadscaleConfig{
						ServerURL:         "https://headscale.example.com",
						ListenAddr:        "0.0.0.0:8080",
						GRPCListenAddr:    "0.0.0.0:50443",
						MetricsListenAddr: "0.0.0.0:9090",
					},
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

			By("Verifying status condition was set with ObservedGeneration")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, typeNamespacedName, createdHeadscale)
				if err != nil {
					return false
				}
				for _, condition := range createdHeadscale.Status.Conditions {
					if condition.Type == readyConditionType &&
						condition.Status == metav1.ConditionTrue &&
						condition.ObservedGeneration == createdHeadscale.Generation {
						return true
					}
				}
				return false
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
					Config: headscalev1beta1.HeadscaleConfig{
						ServerURL:  "https://headscale.example.com",
						ListenAddr: "0.0.0.0:8080",
					},
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
					Config: headscalev1beta1.HeadscaleConfig{
						ServerURL:  "https://headscale.example.com",
						ListenAddr: "0.0.0.0:8080",
					},
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
					Config: headscalev1beta1.HeadscaleConfig{
						ServerURL:  "https://headscale.example.com",
						ListenAddr: "0.0.0.0:8080",
					},
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
				headscale.Spec.Config.ServerURL = "https://new-headscale.example.com"
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
					Config: headscalev1beta1.HeadscaleConfig{
						ServerURL:  "https://headscale.example.com",
						ListenAddr: "0.0.0.0:8080",
					},
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
					Config: headscalev1beta1.HeadscaleConfig{
						ServerURL:  "https://headscale.example.com",
						ListenAddr: "0.0.0.0:8080",
					},
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
					Config: headscalev1beta1.HeadscaleConfig{
						ServerURL:  "https://headscale.example.com",
						ListenAddr: "0.0.0.0:8080",
					},
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
					Config: headscalev1beta1.HeadscaleConfig{
						ServerURL:  "https://headscale.example.com",
						ListenAddr: "0.0.0.0:8080",
					},
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
					Config: headscalev1beta1.HeadscaleConfig{
						ServerURL:  "https://headscale.example.com",
						ListenAddr: "0.0.0.0:8080",
					},
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
			By("Testing computeConfigHashFromSpec with same config")
			config1 := &headscalev1beta1.HeadscaleConfig{
				ServerURL:  "https://example.com",
				ListenAddr: "0.0.0.0:8080",
			}
			config2 := &headscalev1beta1.HeadscaleConfig{
				ServerURL:  "https://example.com",
				ListenAddr: "0.0.0.0:8080",
			}
			hash1 := computeConfigHashFromSpec(config1)
			hash2 := computeConfigHashFromSpec(config2)
			Expect(hash1).To(Equal(hash2))

			By("Testing computeConfigHashFromSpec with different config")
			config3 := &headscalev1beta1.HeadscaleConfig{
				ServerURL:  "https://different.com",
				ListenAddr: "0.0.0.0:8080",
			}
			hash3 := computeConfigHashFromSpec(config3)
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

		It("should preserve empty DERP URLs in marshaled config", func() {
			By("Creating a config with explicitly empty DERP URLs")
			config := &headscalev1beta1.HeadscaleConfig{
				ServerURL:  "https://headscale.example.com",
				ListenAddr: "0.0.0.0:8080",
				DERP: headscalev1beta1.DERPConfig{
					URLs:  []string{},
					Paths: []string{"/etc/derp/derp.yaml"},
				},
			}

			By("Marshaling the config to YAML and unmarshaling to map")
			data, err := yaml.Marshal(config)
			Expect(err).NotTo(HaveOccurred())
			var result map[string]any
			Expect(yaml.Unmarshal(data, &result)).To(Succeed())

			By("Verifying the derp.urls key is present and is an empty list")
			derp, ok := result["derp"].(map[string]any)
			Expect(ok).To(BeTrue(), "expected derp key in config")
			urls, exists := derp["urls"]
			Expect(exists).To(BeTrue(), "expected urls key in derp config")
			Expect(urls).To(BeEmpty(), "expected urls to be an empty list")
		})

		It("should omit DERP URLs when left nil to allow CRD defaults", func() {
			By("Creating a config with nil DERP URLs")
			config := &headscalev1beta1.HeadscaleConfig{
				ServerURL:  "https://headscale.example.com",
				ListenAddr: "0.0.0.0:8080",
				DERP: headscalev1beta1.DERPConfig{
					Paths: []string{"/etc/derp/derp.yaml"},
				},
			}

			By("Marshaling the config to YAML and unmarshaling to map")
			data, err := yaml.Marshal(config)
			Expect(err).NotTo(HaveOccurred())
			var result map[string]any
			Expect(yaml.Unmarshal(data, &result)).To(Succeed())

			By("Verifying the urls key is absent from derp config")
			derp, ok := result["derp"].(map[string]any)
			Expect(ok).To(BeTrue(), "expected derp key in config")
			_, exists := derp["urls"]
			Expect(exists).To(BeFalse(), "nil URLs should be omitted to allow CRD defaults")
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
					Config: headscalev1beta1.HeadscaleConfig{
						ServerURL:  "https://headscale.example.com",
						ListenAddr: "0.0.0.0:8080",
					},
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
