package controller

import (
	"context"
	"encoding/json"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	headscalev1beta1 "github.com/infradohq/headscale-operator/api/v1beta1"
)

var _ = Describe("HeadscaleAutoApprover Controller", func() {
	Context("When reconciling a resource without a parent Headscale", func() {
		const (
			resourceName    = "test-autoapprover"
			namespace       = "default"
			cleanupTimeout  = 10 * time.Second
			cleanupInterval = 250 * time.Millisecond
		)

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: namespace,
		}
		approver := &headscalev1beta1.HeadscaleAutoApprover{}

		BeforeEach(func() {
			err := k8sClient.Get(ctx, typeNamespacedName, approver)
			if err != nil && errors.IsNotFound(err) {
				resource := &headscalev1beta1.HeadscaleAutoApprover{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: namespace,
					},
					Spec: headscalev1beta1.HeadscaleAutoApproverSpec{
						HeadscaleRef: "missing-headscale",
						Routes: []headscalev1beta1.AutoApproverRoute{
							{CIDR: "10.0.0.0/8", Tags: []string{"tag:router"}},
						},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &headscalev1beta1.HeadscaleAutoApprover{}
			if err := k8sClient.Get(ctx, typeNamespacedName, resource); err != nil {
				return
			}
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())

			// Reconcile installs a finalizer; envtest has no live controller, so we
			// drive the deletion path ourselves to actually evict the object and
			// keep the test isolated from subsequent specs. This also exercises
			// handleDeletion with a missing parent Headscale.
			r := &HeadscaleAutoApproverReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() bool {
				return errors.IsNotFound(k8sClient.Get(ctx, typeNamespacedName, &headscalev1beta1.HeadscaleAutoApprover{}))
			}, cleanupTimeout, cleanupInterval).Should(BeTrue())
		})

		It("should set Ready=False with HeadscaleNotFound when the parent is missing", func() {
			r := &HeadscaleAutoApproverReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}
			res, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			// No periodic requeue: a future Headscale Create event fires the
			// watch and re-queues this approver via requeueApproversForHeadscale.
			Expect(res).To(Equal(reconcile.Result{}))

			updated := &headscalev1beta1.HeadscaleAutoApprover{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, updated)).To(Succeed())
			cond := findCondition(updated.Status.Conditions, "Ready")
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal("HeadscaleNotFound"))
		})
	})
})

var _ = Describe("buildPolicyDocument", func() {
	It("merges the inline base, tag owners, and approver entries deterministically", func() {
		base := &headscalev1beta1.ACLPolicyConfig{
			TagOwners: map[string][]string{
				"tag:router": {"group:admin"},
			},
			Inline: `{"acls":[{"action":"accept","src":["*"],"dst":["*:*"]}]}`,
		}
		approvers := []headscalev1beta1.HeadscaleAutoApprover{
			{
				Spec: headscalev1beta1.HeadscaleAutoApproverSpec{
					Routes: []headscalev1beta1.AutoApproverRoute{
						{CIDR: "10.0.0.0/8", Tags: []string{"tag:router"}},
					},
					ExitNodeTags: []string{"tag:exit"},
				},
			},
			{
				Spec: headscalev1beta1.HeadscaleAutoApproverSpec{
					Routes: []headscalev1beta1.AutoApproverRoute{
						{CIDR: "10.0.0.0/8", Tags: []string{"tag:router"}}, // duplicate
						{CIDR: "192.168.0.0/16", Tags: []string{"tag:router"}},
					},
				},
			},
		}

		out, err := buildPolicyDocument(base, approvers)
		Expect(err).NotTo(HaveOccurred())

		var parsed map[string]any
		Expect(json.Unmarshal([]byte(out), &parsed)).To(Succeed())

		Expect(parsed).To(HaveKey("acls"))
		Expect(parsed).To(HaveKey("tagOwners"))

		auto, ok := parsed["autoApprovers"].(map[string]any)
		Expect(ok).To(BeTrue())
		routes, ok := auto["routes"].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(routes).To(HaveKey("10.0.0.0/8"))
		Expect(routes).To(HaveKey("192.168.0.0/16"))
		// Duplicate tag must not be repeated.
		Expect(routes["10.0.0.0/8"]).To(HaveLen(1))
		Expect(auto["exitNode"]).To(ContainElement("tag:exit"))
	})

	It("returns an error when inline base is invalid JSON", func() {
		_, err := buildPolicyDocument(&headscalev1beta1.ACLPolicyConfig{Inline: "not json"}, nil)
		Expect(err).To(HaveOccurred())
	})

	It("accepts HuJSON (comments and trailing commas) in the inline base", func() {
		base := &headscalev1beta1.ACLPolicyConfig{
			Inline: `{
				// Default-allow ACL while we figure out the real ones.
				"acls": [
					{"action": "accept", "src": ["*"], "dst": ["*:*"]},
				],
			}`,
		}
		out, err := buildPolicyDocument(base, nil)
		Expect(err).NotTo(HaveOccurred())

		var parsed map[string]any
		Expect(json.Unmarshal([]byte(out), &parsed)).To(Succeed())
		Expect(parsed).To(HaveKey("acls"))
	})
})

func findCondition(conds []metav1.Condition, t string) *metav1.Condition {
	for i := range conds {
		if conds[i].Type == t {
			return &conds[i]
		}
	}
	return nil
}
