package controller

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	syncv1alpha1 "github.com/ia-eknorr/ignition-sync-operator/api/v1alpha1"
	"github.com/ia-eknorr/ignition-sync-operator/pkg/conditions"
)

func newProfileReconciler() *SyncProfileReconciler {
	return &SyncProfileReconciler{
		Client: k8sClient,
		Scheme: k8sClient.Scheme(),
	}
}

func createSyncProfile(ctx context.Context, name string, spec syncv1alpha1.SyncProfileSpec) {
	profile := &syncv1alpha1.SyncProfile{
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
			profile := &syncv1alpha1.SyncProfile{}
			if err := k8sClient.Get(ctx, nn, profile); err == nil {
				_ = k8sClient.Delete(ctx, profile)
			}
		})

		It("should set Accepted=True for a valid profile", func() {
			createSyncProfile(ctx, profileName, syncv1alpha1.SyncProfileSpec{
				Mappings: []syncv1alpha1.SyncMapping{
					{Source: "services/site/projects", Destination: "projects"},
					{Source: "shared/scripts", Destination: "scripts"},
				},
			})

			r := newProfileReconciler()
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			profile := &syncv1alpha1.SyncProfile{}
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
			profile := &syncv1alpha1.SyncProfile{}
			if err := k8sClient.Get(ctx, nn, profile); err == nil {
				_ = k8sClient.Delete(ctx, profile)
			}
		})

		It("should set Accepted=False for path traversal in source", func() {
			createSyncProfile(ctx, profileName, syncv1alpha1.SyncProfileSpec{
				Mappings: []syncv1alpha1.SyncMapping{
					{Source: "../../../etc/passwd", Destination: "config"},
				},
			})

			r := newProfileReconciler()
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			profile := &syncv1alpha1.SyncProfile{}
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
			profile := &syncv1alpha1.SyncProfile{}
			if err := k8sClient.Get(ctx, nn, profile); err == nil {
				_ = k8sClient.Delete(ctx, profile)
			}
		})

		It("should set Accepted=False for absolute path in source", func() {
			createSyncProfile(ctx, profileName, syncv1alpha1.SyncProfileSpec{
				Mappings: []syncv1alpha1.SyncMapping{
					{Source: "/etc/passwd", Destination: "config"},
				},
			})

			r := newProfileReconciler()
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			profile := &syncv1alpha1.SyncProfile{}
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
			profile := &syncv1alpha1.SyncProfile{}
			if err := k8sClient.Get(ctx, nn, profile); err == nil {
				_ = k8sClient.Delete(ctx, profile)
			}
		})

		It("should reject path traversal in deploymentMode.source", func() {
			createSyncProfile(ctx, profileName, syncv1alpha1.SyncProfileSpec{
				Mappings: []syncv1alpha1.SyncMapping{
					{Source: "services/site", Destination: "site"},
				},
				DeploymentMode: &syncv1alpha1.DeploymentModeSpec{
					Name:   "bad-mode",
					Source: "../../malicious",
				},
			})

			r := newProfileReconciler()
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			profile := &syncv1alpha1.SyncProfile{}
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
			profile := &syncv1alpha1.SyncProfile{}
			if err := k8sClient.Get(ctx, nn, profile); err == nil {
				_ = k8sClient.Delete(ctx, profile)
			}
		})

		It("should accept profile with vars, dependsOn, and dryRun", func() {
			createSyncProfile(ctx, profileName, syncv1alpha1.SyncProfileSpec{
				Mappings: []syncv1alpha1.SyncMapping{
					{Source: "services/site", Destination: "site", Required: true},
				},
				Vars: map[string]string{
					"siteNumber": "1",
					"region":     "us-east",
				},
				DependsOn: []syncv1alpha1.ProfileDependency{
					{ProfileName: "some-base-profile"},
				},
				DryRun: true,
				Paused: true,
			})

			r := newProfileReconciler()
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			profile := &syncv1alpha1.SyncProfile{}
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
})
