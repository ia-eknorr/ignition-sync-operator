/*
Copyright 2026.

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
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	syncv1alpha1 "github.com/inductiveautomation/ignition-sync-operator/api/v1alpha1"
	"github.com/inductiveautomation/ignition-sync-operator/pkg/conditions"
)

// SyncProfileReconciler reconciles a SyncProfile object.
type SyncProfileReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=sync.ignition.io,resources=syncprofiles,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=sync.ignition.io,resources=syncprofiles/status,verbs=get;update;patch

func (r *SyncProfileReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var profile syncv1alpha1.SyncProfile
	if err := r.Get(ctx, req.NamespacedName, &profile); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	base := profile.DeepCopy()

	// Validate the spec
	if err := validateSyncProfile(&profile); err != nil {
		log.Info("SyncProfile validation failed", "name", profile.Name, "error", err.Error())
		setProfileCondition(&profile, conditions.TypeAccepted, metav1.ConditionFalse, conditions.ReasonValidationFailed, err.Error())
	} else {
		setProfileCondition(&profile, conditions.TypeAccepted, metav1.ConditionTrue, conditions.ReasonValidationPassed, "Profile spec is valid")
	}

	profile.Status.ObservedGeneration = profile.Generation

	if err := r.Status().Patch(ctx, &profile, client.MergeFrom(base)); err != nil {
		return ctrl.Result{}, fmt.Errorf("patching SyncProfile status: %w", err)
	}

	return ctrl.Result{}, nil
}

// validateSyncProfile checks that the profile spec is safe and well-formed.
func validateSyncProfile(profile *syncv1alpha1.SyncProfile) error {
	for i, m := range profile.Spec.Mappings {
		if err := validatePath(m.Source, fmt.Sprintf("mappings[%d].source", i)); err != nil {
			return err
		}
		if err := validatePath(m.Destination, fmt.Sprintf("mappings[%d].destination", i)); err != nil {
			return err
		}
	}

	if profile.Spec.DeploymentMode != nil {
		if err := validatePath(profile.Spec.DeploymentMode.Source, "deploymentMode.source"); err != nil {
			return err
		}
	}

	return nil
}

// validatePath rejects absolute paths and path traversal.
func validatePath(p, field string) error {
	if filepath.IsAbs(p) {
		return fmt.Errorf("%s: absolute paths not allowed (%q)", field, p)
	}
	if containsTraversal(p) {
		return fmt.Errorf("%s: path traversal (..) not allowed (%q)", field, p)
	}
	return nil
}

// containsTraversal checks for ".." path components.
func containsTraversal(p string) bool {
	return slices.Contains(strings.Split(filepath.ToSlash(p), "/"), "..")
}

// setProfileCondition sets a condition on the SyncProfile's status.
func setProfileCondition(profile *syncv1alpha1.SyncProfile, condType string, status metav1.ConditionStatus, reason, message string) {
	condition := metav1.Condition{
		Type:               condType,
		Status:             status,
		ObservedGeneration: profile.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             reason,
		Message:            message,
	}

	for i, c := range profile.Status.Conditions {
		if c.Type == condType {
			if c.Status != status {
				profile.Status.Conditions[i] = condition
			} else {
				profile.Status.Conditions[i].Reason = reason
				profile.Status.Conditions[i].Message = message
				profile.Status.Conditions[i].ObservedGeneration = profile.Generation
			}
			return
		}
	}
	profile.Status.Conditions = append(profile.Status.Conditions, condition)
}

// findIgnitionSyncsForProfile returns reconcile requests for all IgnitionSync CRs
// in the same namespace as the changed SyncProfile.
func (r *SyncProfileReconciler) findIgnitionSyncsForProfile(ctx context.Context, obj client.Object) []reconcile.Request {
	var isyncList syncv1alpha1.IgnitionSyncList
	if err := r.List(ctx, &isyncList, client.InNamespace(obj.GetNamespace())); err != nil {
		return nil
	}

	requests := make([]reconcile.Request, 0, len(isyncList.Items))
	for _, isync := range isyncList.Items {
		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      isync.Name,
				Namespace: isync.Namespace,
			},
		})
	}
	return requests
}

// SetupWithManager sets up the SyncProfile controller with the Manager.
func (r *SyncProfileReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&syncv1alpha1.SyncProfile{}).
		// When a SyncProfile changes, also re-reconcile all IgnitionSync CRs
		// in the same namespace so they can update gatewayCount.
		Watches(&syncv1alpha1.SyncProfile{},
			handler.EnqueueRequestsFromMapFunc(r.findIgnitionSyncsForProfile)).
		Named("syncprofile").
		Complete(r)
}
