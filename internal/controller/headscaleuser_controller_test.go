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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	headscalev1beta1 "github.com/infradohq/headscale-operator/api/v1beta1"
)

var _ = Describe("HeadscaleUser Controller", func() {
	Context("When reconciling a HeadscaleUser resource", func() {
		const (
			resourceName  = "test-user"
			headscaleName = "test-headscale"
			namespace     = "default"
			timeout       = time.Second * 10
			interval      = time.Millisecond * 250
		)

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: namespace,
		}

		headscaleNamespacedName := types.NamespacedName{
			Name:      headscaleName,
			Namespace: namespace,
		}

		BeforeEach(func() {
			By("Creating the Headscale instance")
			headscale := &headscalev1beta1.Headscale{
				ObjectMeta: metav1.ObjectMeta{
					Name:      headscaleName,
					Namespace: namespace,
				},
				Spec: headscalev1beta1.HeadscaleSpec{
					Version:  "v0.27.1",
					Replicas: 1,
					Config: headscalev1beta1.HeadscaleConfig{
						ServerURL:         "https://headscale.example.com",
						GRPCListenAddr:    "0.0.0.0:50443",
						MetricsListenAddr: "0.0.0.0:9090",
					},
					PersistentVolumeClaim: headscalev1beta1.PersistentVolumeClaimConfig{
						Size: resource.NewQuantity(128*1024*1024, resource.BinarySI),
					},
					APIKey: headscalev1beta1.APIKeyConfig{
						SecretName: "test-api-key-secret",
					},
				},
			}
			err := k8sClient.Get(ctx, headscaleNamespacedName, &headscalev1beta1.Headscale{})
			if errors.IsNotFound(err) {
				Expect(k8sClient.Create(ctx, headscale)).To(Succeed())
			}

			By("Creating the API key secret")
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-api-key-secret",
					Namespace: namespace,
				},
				Data: map[string][]byte{
					"api-key": []byte("test-api-key-value"),
				},
			}
			secretNamespacedName := types.NamespacedName{
				Name:      "test-api-key-secret",
				Namespace: namespace,
			}
			err = k8sClient.Get(ctx, secretNamespacedName, &corev1.Secret{})
			if errors.IsNotFound(err) {
				Expect(k8sClient.Create(ctx, secret)).To(Succeed())
			}
		})

		AfterEach(func() {
			By("Cleaning up the HeadscaleUser resource")
			resource := &headscalev1beta1.HeadscaleUser{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			if err == nil {
				Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
			}

			By("Cleaning up the Headscale resource")
			headscale := &headscalev1beta1.Headscale{}
			err = k8sClient.Get(ctx, headscaleNamespacedName, headscale)
			if err == nil {
				Expect(k8sClient.Delete(ctx, headscale)).To(Succeed())
			}

			By("Cleaning up the API key secret")
			secret := &corev1.Secret{}
			secretNamespacedName := types.NamespacedName{
				Name:      "test-api-key-secret",
				Namespace: namespace,
			}
			err = k8sClient.Get(ctx, secretNamespacedName, secret)
			if err == nil {
				Expect(k8sClient.Delete(ctx, secret)).To(Succeed())
			}
		})

		It("should successfully create and reconcile a HeadscaleUser", func() {
			By("Creating the HeadscaleUser resource")
			headscaleUser := &headscalev1beta1.HeadscaleUser{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: namespace,
				},
				Spec: headscalev1beta1.HeadscaleUserSpec{
					HeadscaleRef: headscaleName,
					Username:     "testuser",
					DisplayName:  "Test User",
					Email:        "test@example.com",
					PictureURL:   "https://example.com/avatar.jpg",
				},
			}
			Expect(k8sClient.Create(ctx, headscaleUser)).To(Succeed())

			By("Checking that the HeadscaleUser was created")
			createdUser := &headscalev1beta1.HeadscaleUser{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, typeNamespacedName, createdUser)
				return err == nil
			}, timeout, interval).Should(BeTrue())

			By("Reconciling the created resource")
			controllerReconciler := &HeadscaleUserReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Checking that the finalizer was added")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, typeNamespacedName, createdUser)
				if err != nil {
					return false
				}
				return slices.Contains(createdUser.Finalizers, headscaleUserFinalizer)
			}, timeout, interval).Should(BeTrue())
		})

		It("should handle missing Headscale reference", func() {
			By("Creating a HeadscaleUser with non-existent Headscale reference")
			headscaleUser := &headscalev1beta1.HeadscaleUser{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName + "-missing",
					Namespace: namespace,
				},
				Spec: headscalev1beta1.HeadscaleUserSpec{
					HeadscaleRef: "non-existent-headscale",
					Username:     "testuser2",
				},
			}
			Expect(k8sClient.Create(ctx, headscaleUser)).To(Succeed())

			missingTypeNamespacedName := types.NamespacedName{
				Name:      resourceName + "-missing",
				Namespace: namespace,
			}

			By("Reconciling the resource")
			controllerReconciler := &HeadscaleUserReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: missingTypeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Checking that the status reflects the missing Headscale")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, missingTypeNamespacedName, headscaleUser)
				if err != nil {
					return false
				}
				for _, condition := range headscaleUser.Status.Conditions {
					if condition.Type == "Available" &&
						condition.Status == metav1.ConditionFalse &&
						condition.Reason == "HeadscaleNotFound" {
						return true
					}
				}
				return false
			}, timeout, interval).Should(BeTrue())

			By("Cleaning up the test resource")
			Expect(k8sClient.Delete(ctx, headscaleUser)).To(Succeed())
		})

		It("should validate immutable fields", func() {
			By("Creating a HeadscaleUser")
			headscaleUser := &headscalev1beta1.HeadscaleUser{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName + "-immutable",
					Namespace: namespace,
				},
				Spec: headscalev1beta1.HeadscaleUserSpec{
					HeadscaleRef: headscaleName,
					Username:     "immutableuser",
					DisplayName:  "Original Name",
					Email:        "original@example.com",
				},
			}
			Expect(k8sClient.Create(ctx, headscaleUser)).To(Succeed())

			immutableTypeNamespacedName := types.NamespacedName{
				Name:      resourceName + "-immutable",
				Namespace: namespace,
			}

			By("Attempting to update immutable username field")
			Eventually(func() error {
				err := k8sClient.Get(ctx, immutableTypeNamespacedName, headscaleUser)
				if err != nil {
					return err
				}
				headscaleUser.Spec.Username = "changedusername"
				return k8sClient.Update(ctx, headscaleUser)
			}, timeout, interval).Should(HaveOccurred())

			By("Cleaning up the test resource")
			err := k8sClient.Get(ctx, immutableTypeNamespacedName, headscaleUser)
			if err == nil {
				Expect(k8sClient.Delete(ctx, headscaleUser)).To(Succeed())
			}
		})

		It("should handle deletion with finalizer", func() {
			By("Creating a HeadscaleUser")
			headscaleUser := &headscalev1beta1.HeadscaleUser{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName + "-deletion",
					Namespace: namespace,
				},
				Spec: headscalev1beta1.HeadscaleUserSpec{
					HeadscaleRef: headscaleName,
					Username:     "deleteuser",
				},
			}
			Expect(k8sClient.Create(ctx, headscaleUser)).To(Succeed())

			deletionTypeNamespacedName := types.NamespacedName{
				Name:      resourceName + "-deletion",
				Namespace: namespace,
			}

			By("Reconciling to add finalizer")
			controllerReconciler := &HeadscaleUserReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: deletionTypeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying finalizer was added")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, deletionTypeNamespacedName, headscaleUser)
				if err != nil {
					return false
				}
				return slices.Contains(headscaleUser.Finalizers, headscaleUserFinalizer)
			}, timeout, interval).Should(BeTrue())

			By("Deleting the HeadscaleUser")
			Expect(k8sClient.Delete(ctx, headscaleUser)).To(Succeed())

			By("Reconciling the deletion")
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: deletionTypeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
