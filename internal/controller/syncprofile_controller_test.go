package controller

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	stokerv1alpha1 "github.com/ia-eknorr/stoker-operator/api/v1alpha1"
	"github.com/ia-eknorr/stoker-operator/pkg/conditions"
)

func newProfileReconciler() *SyncProfileReconciler {
	return &SyncProfileReconciler{
		Client:   k8sClient,
		Scheme:   k8sClient.Scheme(),
		Recorder: record.NewFakeRecorder(20),
	}
}

func createSyncProfile(ctx context.Context, name string, spec stokerv1alpha1.SyncProfileSpec) {
	profile := &stokerv1alpha1.SyncProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: spec,
	}
	Expect(k8sClient.Create(ctx, profile)).To(Succeed())
}

var _ = Describe("SyncProfile Controller", func() {

	Context("Valid profile", func() {
		const profileName = "test-valid-profile"
		ctx := context.Background()
		nn := types.NamespacedName{Name: profileName, Namespace: "default"}

		AfterEach(func() {
			profile := &stokerv1alpha1.SyncProfile{}
			if err := k8sClient.Get(ctx, nn, profile); err == nil {
				_ = k8sClient.Delete(ctx, profile)
			}
		})

		It("should set Accepted=True for a valid profile", func() {
			createSyncProfile(ctx, profileName, stokerv1alpha1.SyncProfileSpec{
				Mappings: []stokerv1alpha1.SyncMapping{
					{Source: "services/site/projects", Destination: "projects"},
					{Source: "shared/scripts", Destination: "scripts"},
				},
			})

			r := newProfileReconciler()
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			profile := &stokerv1alpha1.SyncProfile{}
			Expect(k8sClient.Get(ctx, nn, profile)).To(Succeed())

			var acceptedCond *metav1.Condition
			for i := range profile.Status.Conditions {
				if profile.Status.Conditions[i].Type == conditions.TypeAccepted {
					acceptedCond = &profile.Status.Conditions[i]
					break
				}
			}
			Expect(acceptedCond).NotTo(BeNil())
			Expect(acceptedCond.Status).To(Equal(metav1.ConditionTrue))
			Expect(acceptedCond.Reason).To(Equal(conditions.ReasonValidationPassed))
			Expect(profile.Status.ObservedGeneration).To(Equal(int64(1)))
		})
	})

	Context("Path traversal", func() {
		const profileName = "test-traversal-profile"
		ctx := context.Background()
		nn := types.NamespacedName{Name: profileName, Namespace: "default"}

		AfterEach(func() {
			profile := &stokerv1alpha1.SyncProfile{}
			if err := k8sClient.Get(ctx, nn, profile); err == nil {
				_ = k8sClient.Delete(ctx, profile)
			}
		})

		It("should set Accepted=False for path traversal in source", func() {
			createSyncProfile(ctx, profileName, stokerv1alpha1.SyncProfileSpec{
				Mappings: []stokerv1alpha1.SyncMapping{
					{Source: "../../../etc/passwd", Destination: "config"},
				},
			})

			r := newProfileReconciler()
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			profile := &stokerv1alpha1.SyncProfile{}
			Expect(k8sClient.Get(ctx, nn, profile)).To(Succeed())

			var acceptedCond *metav1.Condition
			for i := range profile.Status.Conditions {
				if profile.Status.Conditions[i].Type == conditions.TypeAccepted {
					acceptedCond = &profile.Status.Conditions[i]
					break
				}
			}
			Expect(acceptedCond).NotTo(BeNil())
			Expect(acceptedCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(acceptedCond.Message).To(ContainSubstring("traversal"))
		})
	})

	Context("Absolute path", func() {
		const profileName = "test-absolute-profile"
		ctx := context.Background()
		nn := types.NamespacedName{Name: profileName, Namespace: "default"}

		AfterEach(func() {
			profile := &stokerv1alpha1.SyncProfile{}
			if err := k8sClient.Get(ctx, nn, profile); err == nil {
				_ = k8sClient.Delete(ctx, profile)
			}
		})

		It("should set Accepted=False for absolute path in source", func() {
			createSyncProfile(ctx, profileName, stokerv1alpha1.SyncProfileSpec{
				Mappings: []stokerv1alpha1.SyncMapping{
					{Source: "/etc/passwd", Destination: "config"},
				},
			})

			r := newProfileReconciler()
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			profile := &stokerv1alpha1.SyncProfile{}
			Expect(k8sClient.Get(ctx, nn, profile)).To(Succeed())

			var acceptedCond *metav1.Condition
			for i := range profile.Status.Conditions {
				if profile.Status.Conditions[i].Type == conditions.TypeAccepted {
					acceptedCond = &profile.Status.Conditions[i]
					break
				}
			}
			Expect(acceptedCond).NotTo(BeNil())
			Expect(acceptedCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(acceptedCond.Message).To(ContainSubstring("absolute"))
		})
	})

	Context("Deployment mode validation", func() {
		const profileName = "test-depmode-profile"
		ctx := context.Background()
		nn := types.NamespacedName{Name: profileName, Namespace: "default"}

		AfterEach(func() {
			profile := &stokerv1alpha1.SyncProfile{}
			if err := k8sClient.Get(ctx, nn, profile); err == nil {
				_ = k8sClient.Delete(ctx, profile)
			}
		})

		It("should reject path traversal in deploymentMode.source", func() {
			createSyncProfile(ctx, profileName, stokerv1alpha1.SyncProfileSpec{
				Mappings: []stokerv1alpha1.SyncMapping{
					{Source: "services/site", Destination: "site"},
				},
				DeploymentMode: &stokerv1alpha1.DeploymentModeSpec{
					Name:   "bad-mode",
					Source: "../../malicious",
				},
			})

			r := newProfileReconciler()
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			profile := &stokerv1alpha1.SyncProfile{}
			Expect(k8sClient.Get(ctx, nn, profile)).To(Succeed())

			var acceptedCond *metav1.Condition
			for i := range profile.Status.Conditions {
				if profile.Status.Conditions[i].Type == conditions.TypeAccepted {
					acceptedCond = &profile.Status.Conditions[i]
					break
				}
			}
			Expect(acceptedCond).NotTo(BeNil())
			Expect(acceptedCond.Status).To(Equal(metav1.ConditionFalse))
		})
	})

	Context("Profile with optional fields", func() {
		const profileName = "test-optional-fields"
		ctx := context.Background()
		nn := types.NamespacedName{Name: profileName, Namespace: "default"}

		AfterEach(func() {
			for _, name := range []string{profileName, "some-base-profile"} {
				profile := &stokerv1alpha1.SyncProfile{}
				if err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, profile); err == nil {
					_ = k8sClient.Delete(ctx, profile)
				}
			}
		})

		It("should accept profile with vars, dependsOn, and dryRun", func() {
			// Create the dependency profile so validation passes.
			createSyncProfile(ctx, "some-base-profile", stokerv1alpha1.SyncProfileSpec{
				Mappings: []stokerv1alpha1.SyncMapping{
					{Source: "shared/base", Destination: "base"},
				},
			})

			createSyncProfile(ctx, profileName, stokerv1alpha1.SyncProfileSpec{
				Mappings: []stokerv1alpha1.SyncMapping{
					{Source: "services/site", Destination: "site", Required: true},
				},
				Vars: map[string]string{
					"siteNumber": "1",
					"region":     "us-east",
				},
				DependsOn: []stokerv1alpha1.ProfileDependency{
					{ProfileName: "some-base-profile"},
				},
				DryRun: true,
				Paused: true,
			})

			r := newProfileReconciler()
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			profile := &stokerv1alpha1.SyncProfile{}
			Expect(k8sClient.Get(ctx, nn, profile)).To(Succeed())

			var acceptedCond *metav1.Condition
			for i := range profile.Status.Conditions {
				if profile.Status.Conditions[i].Type == conditions.TypeAccepted {
					acceptedCond = &profile.Status.Conditions[i]
					break
				}
			}
			Expect(acceptedCond).NotTo(BeNil())
			Expect(acceptedCond.Status).To(Equal(metav1.ConditionTrue))

			// Verify fields roundtrip
			Expect(profile.Spec.Vars["siteNumber"]).To(Equal("1"))
			Expect(profile.Spec.DependsOn[0].ProfileName).To(Equal("some-base-profile"))
			Expect(profile.Spec.DryRun).To(BeTrue())
			Expect(profile.Spec.Paused).To(BeTrue())
			Expect(profile.Spec.Mappings[0].Required).To(BeTrue())
		})
	})

	// ── Dependency Validation Tests ──────────────────────────────────────

	Context("Self-dependency cycle", func() {
		const profileName = "test-self-dep"
		ctx := context.Background()
		nn := types.NamespacedName{Name: profileName, Namespace: "default"}

		AfterEach(func() {
			profile := &stokerv1alpha1.SyncProfile{}
			if err := k8sClient.Get(ctx, nn, profile); err == nil {
				_ = k8sClient.Delete(ctx, profile)
			}
		})

		It("should reject a profile that depends on itself", func() {
			createSyncProfile(ctx, profileName, stokerv1alpha1.SyncProfileSpec{
				Mappings: []stokerv1alpha1.SyncMapping{
					{Source: "src", Destination: "dst"},
				},
				DependsOn: []stokerv1alpha1.ProfileDependency{
					{ProfileName: profileName},
				},
			})

			r := newProfileReconciler()
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			profile := &stokerv1alpha1.SyncProfile{}
			Expect(k8sClient.Get(ctx, nn, profile)).To(Succeed())

			cond := findAcceptedCondition(profile)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(conditions.ReasonCycleDetected))
			Expect(cond.Message).To(ContainSubstring(profileName))
		})
	})

	Context("Direct cycle (A→B→A)", func() {
		ctx := context.Background()
		profileA := "test-cycle-a"
		profileB := "test-cycle-b"

		AfterEach(func() {
			for _, name := range []string{profileA, profileB} {
				profile := &stokerv1alpha1.SyncProfile{}
				if err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, profile); err == nil {
					_ = k8sClient.Delete(ctx, profile)
				}
			}
		})

		It("should detect a two-node cycle", func() {
			createSyncProfile(ctx, profileB, stokerv1alpha1.SyncProfileSpec{
				Mappings: []stokerv1alpha1.SyncMapping{{Source: "src", Destination: "dst"}},
				DependsOn: []stokerv1alpha1.ProfileDependency{
					{ProfileName: profileA},
				},
			})
			createSyncProfile(ctx, profileA, stokerv1alpha1.SyncProfileSpec{
				Mappings: []stokerv1alpha1.SyncMapping{{Source: "src", Destination: "dst"}},
				DependsOn: []stokerv1alpha1.ProfileDependency{
					{ProfileName: profileB},
				},
			})

			r := newProfileReconciler()
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: profileA, Namespace: "default"}})
			Expect(err).NotTo(HaveOccurred())

			profile := &stokerv1alpha1.SyncProfile{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: profileA, Namespace: "default"}, profile)).To(Succeed())

			cond := findAcceptedCondition(profile)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(conditions.ReasonCycleDetected))
		})
	})

	Context("Three-node cycle (A→B→C→A)", func() {
		ctx := context.Background()
		names := []string{"test-tri-a", "test-tri-b", "test-tri-c"}

		AfterEach(func() {
			for _, name := range names {
				profile := &stokerv1alpha1.SyncProfile{}
				if err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, profile); err == nil {
					_ = k8sClient.Delete(ctx, profile)
				}
			}
		})

		It("should detect a three-node cycle", func() {
			mapping := []stokerv1alpha1.SyncMapping{{Source: "src", Destination: "dst"}}

			createSyncProfile(ctx, "test-tri-c", stokerv1alpha1.SyncProfileSpec{
				Mappings:  mapping,
				DependsOn: []stokerv1alpha1.ProfileDependency{{ProfileName: "test-tri-a"}},
			})
			createSyncProfile(ctx, "test-tri-b", stokerv1alpha1.SyncProfileSpec{
				Mappings:  mapping,
				DependsOn: []stokerv1alpha1.ProfileDependency{{ProfileName: "test-tri-c"}},
			})
			createSyncProfile(ctx, "test-tri-a", stokerv1alpha1.SyncProfileSpec{
				Mappings:  mapping,
				DependsOn: []stokerv1alpha1.ProfileDependency{{ProfileName: "test-tri-b"}},
			})

			r := newProfileReconciler()
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: "test-tri-a", Namespace: "default"}})
			Expect(err).NotTo(HaveOccurred())

			profile := &stokerv1alpha1.SyncProfile{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "test-tri-a", Namespace: "default"}, profile)).To(Succeed())

			cond := findAcceptedCondition(profile)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(conditions.ReasonCycleDetected))
			Expect(cond.Message).To(ContainSubstring("→"))
		})
	})

	Context("Missing dependency", func() {
		const profileName = "test-missing-dep"
		ctx := context.Background()
		nn := types.NamespacedName{Name: profileName, Namespace: "default"}

		AfterEach(func() {
			profile := &stokerv1alpha1.SyncProfile{}
			if err := k8sClient.Get(ctx, nn, profile); err == nil {
				_ = k8sClient.Delete(ctx, profile)
			}
		})

		It("should reject a profile with a nonexistent dependency", func() {
			createSyncProfile(ctx, profileName, stokerv1alpha1.SyncProfileSpec{
				Mappings: []stokerv1alpha1.SyncMapping{
					{Source: "src", Destination: "dst"},
				},
				DependsOn: []stokerv1alpha1.ProfileDependency{
					{ProfileName: "nonexistent-profile"},
				},
			})

			r := newProfileReconciler()
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			profile := &stokerv1alpha1.SyncProfile{}
			Expect(k8sClient.Get(ctx, nn, profile)).To(Succeed())

			cond := findAcceptedCondition(profile)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(conditions.ReasonDependencyNotFound))
			Expect(cond.Message).To(ContainSubstring("nonexistent-profile"))
		})
	})

	Context("Valid dependency chain", func() {
		ctx := context.Background()
		base := "test-valid-base"
		child := "test-valid-child"

		AfterEach(func() {
			for _, name := range []string{base, child} {
				profile := &stokerv1alpha1.SyncProfile{}
				if err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, profile); err == nil {
					_ = k8sClient.Delete(ctx, profile)
				}
			}
		})

		It("should accept a valid dependency chain", func() {
			mapping := []stokerv1alpha1.SyncMapping{{Source: "src", Destination: "dst"}}

			createSyncProfile(ctx, base, stokerv1alpha1.SyncProfileSpec{
				Mappings: mapping,
			})
			createSyncProfile(ctx, child, stokerv1alpha1.SyncProfileSpec{
				Mappings:  mapping,
				DependsOn: []stokerv1alpha1.ProfileDependency{{ProfileName: base}},
			})

			r := newProfileReconciler()

			// Reconcile base first.
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: base, Namespace: "default"}})
			Expect(err).NotTo(HaveOccurred())

			// Reconcile child.
			_, err = r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: child, Namespace: "default"}})
			Expect(err).NotTo(HaveOccurred())

			profile := &stokerv1alpha1.SyncProfile{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: child, Namespace: "default"}, profile)).To(Succeed())

			cond := findAcceptedCondition(profile)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal(conditions.ReasonValidationPassed))
		})
	})
})

// findAcceptedCondition returns the Accepted condition, or nil.
func findAcceptedCondition(profile *stokerv1alpha1.SyncProfile) *metav1.Condition {
	for i := range profile.Status.Conditions {
		if profile.Status.Conditions[i].Type == conditions.TypeAccepted {
			return &profile.Status.Conditions[i]
		}
	}
	return nil
}
