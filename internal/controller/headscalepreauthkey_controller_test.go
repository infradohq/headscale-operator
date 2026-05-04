package controller

import (
	"context"
	"slices"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	headscalev1beta1 "github.com/infradohq/headscale-operator/api/v1beta1"
)

var _ = Describe("HeadscalePreAuthKey Controller", func() {
	Context("When reconciling a HeadscalePreAuthKey resource", func() {
		const (
			resourceName            = "test-preauthkey"
			headscaleName           = "test-headscale-pak"
			headscaleUserName       = "test-user-pak"
			namespace               = "default"
			timeout                 = time.Second * 10
			interval                = time.Millisecond * 250
			readyConditionType      = "Ready"
			headscaleNotFoundReason = "HeadscaleNotFound"
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

		headscaleUserNamespacedName := types.NamespacedName{
			Name:      headscaleUserName,
			Namespace: namespace,
		}

		// Helper function to create a test Headscale instance
		createHeadscaleInstance := func() {
			By("Creating the Headscale instance")
			headscale := &headscalev1beta1.Headscale{
				ObjectMeta: metav1.ObjectMeta{
					Name:      headscaleName,
					Namespace: namespace,
				},
				Spec: headscalev1beta1.HeadscaleSpec{
					Version:  "v0.28.0",
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
						SecretName: "test-api-key-secret-pak",
					},
				},
			}
			err := k8sClient.Get(ctx, headscaleNamespacedName, &headscalev1beta1.Headscale{})
			if errors.IsNotFound(err) {
				Expect(k8sClient.Create(ctx, headscale)).To(Succeed())
			}
		}

		// Helper function to create API key secret
		createAPIKeySecret := func() {
			By("Creating the API key secret")
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-api-key-secret-pak",
					Namespace: namespace,
				},
				Data: map[string][]byte{
					"api-key": []byte("test-api-key-value"),
				},
			}
			secretNamespacedName := types.NamespacedName{
				Name:      "test-api-key-secret-pak",
				Namespace: namespace,
			}
			err := k8sClient.Get(ctx, secretNamespacedName, &corev1.Secret{})
			if errors.IsNotFound(err) {
				Expect(k8sClient.Create(ctx, secret)).To(Succeed())
			}
		}

		// Helper function to create a test HeadscaleUser instance
		createHeadscaleUser := func() {
			By("Creating the HeadscaleUser instance")
			user := &headscalev1beta1.HeadscaleUser{
				ObjectMeta: metav1.ObjectMeta{
					Name:      headscaleUserName,
					Namespace: namespace,
				},
				Spec: headscalev1beta1.HeadscaleUserSpec{
					HeadscaleRef: headscaleName,
					Username:     "testuser",
				},
			}
			err := k8sClient.Get(ctx, headscaleUserNamespacedName, &headscalev1beta1.HeadscaleUser{})
			if errors.IsNotFound(err) {
				Expect(k8sClient.Create(ctx, user)).To(Succeed())
			}

			// Set UserID in status to simulate a ready user
			By("Setting UserID in HeadscaleUser status")
			Eventually(func() error {
				err := k8sClient.Get(ctx, headscaleUserNamespacedName, user)
				if err != nil {
					return err
				}
				user.Status.UserID = "123"
				return k8sClient.Status().Update(ctx, user)
			}, timeout, interval).Should(Succeed())
		}

		// Common cleanup function
		cleanupResources := func() {
			By("Cleaning up the HeadscalePreAuthKey resource")
			resource := &headscalev1beta1.HeadscalePreAuthKey{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			if err == nil {
				Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
			}

			By("Cleaning up the HeadscaleUser resource")
			user := &headscalev1beta1.HeadscaleUser{}
			err = k8sClient.Get(ctx, headscaleUserNamespacedName, user)
			if err == nil {
				Expect(k8sClient.Delete(ctx, user)).To(Succeed())
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
				Name:      "test-api-key-secret-pak",
				Namespace: namespace,
			}
			err = k8sClient.Get(ctx, secretNamespacedName, secret)
			if err == nil {
				Expect(k8sClient.Delete(ctx, secret)).To(Succeed())
			}
		}

		BeforeEach(func() {
			createHeadscaleInstance()
			createAPIKeySecret()
			createHeadscaleUser()
		})

		AfterEach(func() {
			cleanupResources()
		})

		It("should successfully create and reconcile a HeadscalePreAuthKey with HeadscaleUserRef", func() {
			By("Creating the HeadscalePreAuthKey resource with HeadscaleUserRef")
			preAuthKey := &headscalev1beta1.HeadscalePreAuthKey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: namespace,
				},
				Spec: headscalev1beta1.HeadscalePreAuthKeySpec{
					HeadscaleRef:     headscaleName,
					HeadscaleUserRef: headscaleUserName,
					Expiration:       "1h",
					Reusable:         false,
					Ephemeral:        false,
					Tags:             []string{"tag:test"},
				},
			}
			Expect(k8sClient.Create(ctx, preAuthKey)).To(Succeed())

			By("Checking that the HeadscalePreAuthKey was created")
			createdKey := &headscalev1beta1.HeadscalePreAuthKey{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, typeNamespacedName, createdKey)
				return err == nil
			}, timeout, interval).Should(BeTrue())

			By("Reconciling the created resource")
			controllerReconciler := &HeadscalePreAuthKeyReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Checking that the finalizer was added")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, typeNamespacedName, createdKey)
				if err != nil {
					return false
				}
				return slices.Contains(createdKey.Finalizers, headscalePreAuthKeyFinalizer)
			}, timeout, interval).Should(BeTrue())

			By("Verifying owner reference was set for HeadscaleUser")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, typeNamespacedName, createdKey)
				if err != nil {
					return false
				}
				for _, ref := range createdKey.GetOwnerReferences() {
					if ref.Kind == "HeadscaleUser" && ref.Name == headscaleUserName {
						return true
					}
				}
				return false
			}, timeout, interval).Should(BeTrue())
		})

		It("should successfully create and reconcile a HeadscalePreAuthKey with UserID", func() {
			By("Creating the HeadscalePreAuthKey resource with direct UserID")
			preAuthKeyDirectID := &headscalev1beta1.HeadscalePreAuthKey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName + "-userid",
					Namespace: namespace,
				},
				Spec: headscalev1beta1.HeadscalePreAuthKeySpec{
					HeadscaleRef: headscaleName,
					UserID:       123,
					Expiration:   "2h",
					Reusable:     true,
					Ephemeral:    true,
				},
			}
			Expect(k8sClient.Create(ctx, preAuthKeyDirectID)).To(Succeed())

			directIDNamespacedName := types.NamespacedName{
				Name:      resourceName + "-userid",
				Namespace: namespace,
			}

			By("Reconciling the created resource")
			controllerReconciler := &HeadscalePreAuthKeyReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: directIDNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Checking that the finalizer was added")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, directIDNamespacedName, preAuthKeyDirectID)
				if err != nil {
					return false
				}
				return slices.Contains(preAuthKeyDirectID.Finalizers, headscalePreAuthKeyFinalizer)
			}, timeout, interval).Should(BeTrue())

			By("Cleaning up the test resource")
			Expect(k8sClient.Delete(ctx, preAuthKeyDirectID)).To(Succeed())
		})

		It("should handle missing Headscale reference", func() {
			By("Creating a HeadscalePreAuthKey with non-existent Headscale reference")
			preAuthKey := &headscalev1beta1.HeadscalePreAuthKey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName + "-missing-hs",
					Namespace: namespace,
				},
				Spec: headscalev1beta1.HeadscalePreAuthKeySpec{
					HeadscaleRef:     "non-existent-headscale",
					HeadscaleUserRef: headscaleUserName,
					Expiration:       "1h",
				},
			}
			Expect(k8sClient.Create(ctx, preAuthKey)).To(Succeed())

			missingHSNamespacedName := types.NamespacedName{
				Name:      resourceName + "-missing-hs",
				Namespace: namespace,
			}

			By("Reconciling the resource")
			controllerReconciler := &HeadscalePreAuthKeyReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: missingHSNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Checking that the status reflects the missing Headscale")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, missingHSNamespacedName, preAuthKey)
				if err != nil {
					return false
				}
				for _, condition := range preAuthKey.Status.Conditions {
					if condition.Type == readyConditionType &&
						condition.Status == metav1.ConditionFalse &&
						condition.Reason == headscaleNotFoundReason &&
						condition.ObservedGeneration == preAuthKey.Generation {
						return true
					}
				}
				return false
			}, timeout, interval).Should(BeTrue())

			By("Cleaning up the test resource")
			Expect(k8sClient.Delete(ctx, preAuthKey)).To(Succeed())
		})

		It("should handle missing HeadscaleUser reference", func() {
			By("Creating a HeadscalePreAuthKey with non-existent HeadscaleUser reference")
			preAuthKey := &headscalev1beta1.HeadscalePreAuthKey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName + "-missing-user",
					Namespace: namespace,
				},
				Spec: headscalev1beta1.HeadscalePreAuthKeySpec{
					HeadscaleRef:     headscaleName,
					HeadscaleUserRef: "non-existent-user",
					Expiration:       "1h",
				},
			}
			Expect(k8sClient.Create(ctx, preAuthKey)).To(Succeed())

			missingUserNamespacedName := types.NamespacedName{
				Name:      resourceName + "-missing-user",
				Namespace: namespace,
			}

			By("Reconciling the resource")
			controllerReconciler := &HeadscalePreAuthKeyReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: missingUserNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Checking that the status reflects the missing HeadscaleUser")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, missingUserNamespacedName, preAuthKey)
				if err != nil {
					return false
				}
				for _, condition := range preAuthKey.Status.Conditions {
					if condition.Type == readyConditionType &&
						condition.Status == metav1.ConditionFalse &&
						condition.Reason == "UserNotFound" &&
						condition.ObservedGeneration == preAuthKey.Generation {
						return true
					}
				}
				return false
			}, timeout, interval).Should(BeTrue())

			By("Cleaning up the test resource")
			Expect(k8sClient.Delete(ctx, preAuthKey)).To(Succeed())
		})

		It("should handle HeadscaleUser not ready (UserID not set)", func() {
			By("Creating a HeadscaleUser without UserID")
			userNotReady := &headscalev1beta1.HeadscaleUser{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "user-not-ready",
					Namespace: namespace,
				},
				Spec: headscalev1beta1.HeadscaleUserSpec{
					HeadscaleRef: headscaleName,
					Username:     "notreadyuser",
				},
			}
			Expect(k8sClient.Create(ctx, userNotReady)).To(Succeed())

			By("Creating a HeadscalePreAuthKey referencing the not-ready user")
			preAuthKey := &headscalev1beta1.HeadscalePreAuthKey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName + "-user-not-ready",
					Namespace: namespace,
				},
				Spec: headscalev1beta1.HeadscalePreAuthKeySpec{
					HeadscaleRef:     headscaleName,
					HeadscaleUserRef: "user-not-ready",
					Expiration:       "1h",
				},
			}
			Expect(k8sClient.Create(ctx, preAuthKey)).To(Succeed())

			notReadyNamespacedName := types.NamespacedName{
				Name:      resourceName + "-user-not-ready",
				Namespace: namespace,
			}

			By("Reconciling the resource")
			controllerReconciler := &HeadscalePreAuthKeyReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: notReadyNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Checking that the status reflects the user not ready")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, notReadyNamespacedName, preAuthKey)
				if err != nil {
					return false
				}
				for _, condition := range preAuthKey.Status.Conditions {
					if condition.Type == readyConditionType &&
						condition.Status == metav1.ConditionFalse &&
						condition.Reason == "UserNotReady" &&
						condition.ObservedGeneration == preAuthKey.Generation {
						return true
					}
				}
				return false
			}, timeout, interval).Should(BeTrue())

			By("Cleaning up test resources")
			Expect(k8sClient.Delete(ctx, preAuthKey)).To(Succeed())
			Expect(k8sClient.Delete(ctx, userNotReady)).To(Succeed())
		})

		It("should preserve LastTransitionTime when reason changes but status stays the same", func() {
			By("Creating a HeadscalePreAuthKey referencing a non-existent user")
			preAuthKey := &headscalev1beta1.HeadscalePreAuthKey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName + "-ltt",
					Namespace: namespace,
				},
				Spec: headscalev1beta1.HeadscalePreAuthKeySpec{
					HeadscaleRef:     headscaleName,
					HeadscaleUserRef: "user-ltt-not-ready",
					Expiration:       "1h",
				},
			}
			Expect(k8sClient.Create(ctx, preAuthKey)).To(Succeed())

			lttNamespacedName := types.NamespacedName{
				Name:      resourceName + "-ltt",
				Namespace: namespace,
			}

			controllerReconciler := &HeadscalePreAuthKeyReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			By("Reconciling - should get UserNotFound")
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: lttNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Recording the initial LastTransitionTime for Ready=False/UserNotFound")
			var initialTransitionTime metav1.Time
			Eventually(func() bool {
				err := k8sClient.Get(ctx, lttNamespacedName, preAuthKey)
				if err != nil {
					return false
				}
				for _, condition := range preAuthKey.Status.Conditions {
					if condition.Type == readyConditionType &&
						condition.Status == metav1.ConditionFalse &&
						condition.Reason == "UserNotFound" {
						initialTransitionTime = condition.LastTransitionTime
						return true
					}
				}
				return false
			}, timeout, interval).Should(BeTrue())
			Expect(initialTransitionTime.IsZero()).To(BeFalse())

			By("Creating the user without UserID (not ready)")
			userLtt := &headscalev1beta1.HeadscaleUser{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "user-ltt-not-ready",
					Namespace: namespace,
				},
				Spec: headscalev1beta1.HeadscaleUserSpec{
					HeadscaleRef: headscaleName,
					Username:     "lttnotreadyuser",
				},
			}
			Expect(k8sClient.Create(ctx, userLtt)).To(Succeed())

			By("Reconciling again - should get UserNotReady (Status stays False, Reason changes)")
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: lttNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying condition changed to UserNotReady with preserved LastTransitionTime")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, lttNamespacedName, preAuthKey)
				if err != nil {
					return false
				}
				for _, condition := range preAuthKey.Status.Conditions {
					if condition.Type == readyConditionType &&
						condition.Status == metav1.ConditionFalse &&
						condition.Reason == "UserNotReady" {
						return true
					}
				}
				return false
			}, timeout, interval).Should(BeTrue())

			Expect(k8sClient.Get(ctx, lttNamespacedName, preAuthKey)).To(Succeed())
			foundReady := false
			for _, condition := range preAuthKey.Status.Conditions {
				if condition.Type == readyConditionType {
					foundReady = true
					Expect(condition.LastTransitionTime).To(Equal(initialTransitionTime),
						"LastTransitionTime should not change when status remains False")
					Expect(condition.ObservedGeneration).To(Equal(preAuthKey.Generation),
						"ObservedGeneration should match the object's generation")
				}
			}
			Expect(foundReady).To(BeTrue(), "expected Ready condition to be present")

			By("Cleaning up test resources")
			Expect(k8sClient.Delete(ctx, preAuthKey)).To(Succeed())
			Expect(k8sClient.Delete(ctx, userLtt)).To(Succeed())
		})

		It("should reject spec with neither user reference nor tags", func() {
			By("Attempting to create a HeadscalePreAuthKey without HeadscaleUserRef, UserID, or Tags")
			preAuthKey := &headscalev1beta1.HeadscalePreAuthKey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName + "-no-user",
					Namespace: namespace,
				},
				Spec: headscalev1beta1.HeadscalePreAuthKeySpec{
					HeadscaleRef: headscaleName,
					Expiration:   "1h",
				},
			}

			By("Verifying that creation fails due to validation")
			err := k8sClient.Create(ctx, preAuthKey)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("spec.tags must be non-empty when neither spec.headscaleUserRef nor spec.userId is set"))
		})

		It("should reject spec with both HeadscaleUserRef and UserID", func() {
			By("Attempting to create a HeadscalePreAuthKey with both HeadscaleUserRef and UserID")
			preAuthKey := &headscalev1beta1.HeadscalePreAuthKey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName + "-both-user",
					Namespace: namespace,
				},
				Spec: headscalev1beta1.HeadscalePreAuthKeySpec{
					HeadscaleRef:     headscaleName,
					HeadscaleUserRef: headscaleUserName,
					UserID:           456,
					Expiration:       "1h",
				},
			}

			By("Verifying that creation fails due to validation")
			err := k8sClient.Create(ctx, preAuthKey)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("spec.headscaleUserRef and spec.userId are mutually exclusive"))
		})

		It("should accept and reconcile a tags-only HeadscalePreAuthKey (no user)", func() {
			By("Creating a HeadscalePreAuthKey with tags but no user reference")
			preAuthKey := &headscalev1beta1.HeadscalePreAuthKey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName + "-tags-only",
					Namespace: namespace,
				},
				Spec: headscalev1beta1.HeadscalePreAuthKeySpec{
					HeadscaleRef: headscaleName,
					Expiration:   "1h",
					Tags:         []string{"tag:ci"},
				},
			}
			Expect(k8sClient.Create(ctx, preAuthKey)).To(Succeed())

			tagsOnlyNamespacedName := types.NamespacedName{
				Name:      resourceName + "-tags-only",
				Namespace: namespace,
			}

			By("Reconciling the created resource")
			controllerReconciler := &HeadscalePreAuthKeyReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: tagsOnlyNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Checking that the finalizer was added")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, tagsOnlyNamespacedName, preAuthKey)
				if err != nil {
					return false
				}
				return slices.Contains(preAuthKey.Finalizers, headscalePreAuthKeyFinalizer)
			}, timeout, interval).Should(BeTrue())

			By("Verifying no HeadscaleUser owner reference was set")
			Expect(k8sClient.Get(ctx, tagsOnlyNamespacedName, preAuthKey)).To(Succeed())
			for _, ref := range preAuthKey.GetOwnerReferences() {
				Expect(ref.Kind).NotTo(Equal("HeadscaleUser"))
			}

			By("Cleaning up the test resource")
			Expect(k8sClient.Delete(ctx, preAuthKey)).To(Succeed())
		})

		It("should handle deletion with finalizer", func() {
			By("Creating a HeadscalePreAuthKey")
			preAuthKey := &headscalev1beta1.HeadscalePreAuthKey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName + "-deletion",
					Namespace: namespace,
				},
				Spec: headscalev1beta1.HeadscalePreAuthKeySpec{
					HeadscaleRef:     headscaleName,
					HeadscaleUserRef: headscaleUserName,
					Expiration:       "1h",
				},
			}
			Expect(k8sClient.Create(ctx, preAuthKey)).To(Succeed())

			deletionNamespacedName := types.NamespacedName{
				Name:      resourceName + "-deletion",
				Namespace: namespace,
			}

			By("Reconciling to add finalizer")
			controllerReconciler := &HeadscalePreAuthKeyReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: deletionNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying finalizer was added")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, deletionNamespacedName, preAuthKey)
				if err != nil {
					return false
				}
				return slices.Contains(preAuthKey.Finalizers, headscalePreAuthKeyFinalizer)
			}, timeout, interval).Should(BeTrue())

			By("Deleting the HeadscalePreAuthKey")
			Expect(k8sClient.Delete(ctx, preAuthKey)).To(Succeed())

			By("Reconciling the deletion")
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: deletionNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("should handle deletion with KeyID in status when Headscale instance is missing", func() {
			By("Creating a HeadscalePreAuthKey with a non-existent Headscale reference")
			preAuthKey := &headscalev1beta1.HeadscalePreAuthKey{
				ObjectMeta: metav1.ObjectMeta{
					Name:       resourceName + "-deletion-keyid",
					Namespace:  namespace,
					Finalizers: []string{headscalePreAuthKeyFinalizer},
				},
				Spec: headscalev1beta1.HeadscalePreAuthKeySpec{
					HeadscaleRef:     "non-existent-headscale",
					HeadscaleUserRef: headscaleUserName,
					Expiration:       "1h",
				},
			}
			Expect(k8sClient.Create(ctx, preAuthKey)).To(Succeed())

			deletionKeyIDNamespacedName := types.NamespacedName{
				Name:      resourceName + "-deletion-keyid",
				Namespace: namespace,
			}

			By("Setting KeyID in status to simulate a created key")
			Eventually(func() error {
				err := k8sClient.Get(ctx, deletionKeyIDNamespacedName, preAuthKey)
				if err != nil {
					return err
				}
				preAuthKey.Status.KeyID = "12345"
				return k8sClient.Status().Update(ctx, preAuthKey)
			}, timeout, interval).Should(Succeed())

			By("Deleting the HeadscalePreAuthKey")
			Expect(k8sClient.Delete(ctx, preAuthKey)).To(Succeed())

			By("Reconciling the deletion - should remove finalizer since Headscale instance is missing")
			controllerReconciler := &HeadscalePreAuthKeyReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: deletionKeyIDNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying the resource is deleted (finalizer was removed)")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, deletionKeyIDNamespacedName, preAuthKey)
				return errors.IsNotFound(err)
			}, timeout, interval).Should(BeTrue())
		})

		It("should create secret with custom name when SecretName is specified", func() {
			By("Creating a HeadscalePreAuthKey with custom SecretName")
			customSecretName := "custom-secret-name"
			preAuthKey := &headscalev1beta1.HeadscalePreAuthKey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName + "-custom-secret",
					Namespace: namespace,
				},
				Spec: headscalev1beta1.HeadscalePreAuthKeySpec{
					HeadscaleRef:     headscaleName,
					HeadscaleUserRef: headscaleUserName,
					Expiration:       "1h",
					SecretName:       customSecretName,
				},
			}
			Expect(k8sClient.Create(ctx, preAuthKey)).To(Succeed())

			customSecretNamespacedName := types.NamespacedName{
				Name:      resourceName + "-custom-secret",
				Namespace: namespace,
			}

			By("Reconciling the created resource")
			controllerReconciler := &HeadscalePreAuthKeyReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: customSecretNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Cleaning up the test resource")
			Expect(k8sClient.Delete(ctx, preAuthKey)).To(Succeed())

			// Clean up custom secret if it was created
			secret := &corev1.Secret{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: customSecretName, Namespace: namespace}, secret)
			if err == nil {
				Expect(k8sClient.Delete(ctx, secret)).To(Succeed())
			}
		})

		It("should handle reconciliation when resource is not found", func() {
			By("Reconciling a non-existent resource")
			controllerReconciler := &HeadscalePreAuthKeyReconciler{
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

		It("should not requeue if PreAuthKey already exists (Status.KeyID is set)", func() {
			By("Creating a HeadscalePreAuthKey")
			preAuthKey := &headscalev1beta1.HeadscalePreAuthKey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName + "-existing",
					Namespace: namespace,
				},
				Spec: headscalev1beta1.HeadscalePreAuthKeySpec{
					HeadscaleRef:     headscaleName,
					HeadscaleUserRef: headscaleUserName,
					Expiration:       "1h",
				},
			}
			Expect(k8sClient.Create(ctx, preAuthKey)).To(Succeed())

			existingNamespacedName := types.NamespacedName{
				Name:      resourceName + "-existing",
				Namespace: namespace,
			}

			By("Setting KeyID in status to simulate existing key")
			Eventually(func() error {
				err := k8sClient.Get(ctx, existingNamespacedName, preAuthKey)
				if err != nil {
					return err
				}
				preAuthKey.Status.KeyID = "789"
				return k8sClient.Status().Update(ctx, preAuthKey)
			}, timeout, interval).Should(Succeed())

			By("Reconciling the resource")
			controllerReconciler := &HeadscalePreAuthKeyReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			result, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: existingNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			By("Cleaning up the test resource")
			Expect(k8sClient.Delete(ctx, preAuthKey)).To(Succeed())
		})
	})

	Context("Helper function tests", func() {
		It("should correctly identify PreAuthKey not found errors", func() {
			By("Testing isPreAuthKeyNotFoundError with gRPC NotFound error")
			err := status.Error(codes.NotFound, "PreAuthKey not found")
			Expect(isPreAuthKeyNotFoundError(err)).To(BeTrue())

			By("Testing isPreAuthKeyNotFoundError with non-matching gRPC error")
			err = status.Error(codes.Internal, "some other error")
			Expect(isPreAuthKeyNotFoundError(err)).To(BeFalse())

			By("Testing isPreAuthKeyNotFoundError with non-gRPC error")
			err = errors.NewBadRequest("some other error")
			Expect(isPreAuthKeyNotFoundError(err)).To(BeFalse())

			By("Testing isPreAuthKeyNotFoundError with nil error")
			Expect(isPreAuthKeyNotFoundError(nil)).To(BeFalse())
		})
	})
})
