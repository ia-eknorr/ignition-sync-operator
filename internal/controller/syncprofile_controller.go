package controller

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	stokerv1alpha1 "github.com/ia-eknorr/stoker-operator/api/v1alpha1"
	"github.com/ia-eknorr/stoker-operator/pkg/conditions"
)

// SyncProfileReconciler reconciles a SyncProfile object.
type SyncProfileReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=stoker.io,resources=syncprofiles,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=stoker.io,resources=syncprofiles/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *SyncProfileReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var profile stokerv1alpha1.SyncProfile
	if err := r.Get(ctx, req.NamespacedName, &profile); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	base := profile.DeepCopy()

	// Validate the spec
	if err := validateSyncProfile(&profile); err != nil {
		log.Info("SyncProfile validation failed", "name", profile.Name, "error", err.Error())
		setProfileCondition(&profile, conditions.TypeAccepted, metav1.ConditionFalse, conditions.ReasonValidationFailed, err.Error())
		r.Recorder.Eventf(&profile, corev1.EventTypeWarning, conditions.ReasonValidationFailed, "Validation failed: %s", err.Error())
	} else if depErr := r.validateDependencies(ctx, &profile); depErr != nil {
		log.Info("SyncProfile dependency validation failed", "name", profile.Name, "error", depErr.Error())
		setProfileCondition(&profile, conditions.TypeAccepted, metav1.ConditionFalse, depErr.reason, depErr.Error())
		r.Recorder.Eventf(&profile, corev1.EventTypeWarning, depErr.reason, "%s", depErr.Error())
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
func validateSyncProfile(profile *stokerv1alpha1.SyncProfile) error {
	for i, m := range profile.Spec.Mappings {
		if err := validatePath(m.Source, fmt.Sprintf("mappings[%d].source", i)); err != nil {
			return err
		}
		if err := validatePath(m.Destination, fmt.Sprintf("mappings[%d].destination", i)); err != nil {
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
func setProfileCondition(profile *stokerv1alpha1.SyncProfile, condType string, status metav1.ConditionStatus, reason, message string) {
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

// dependencyError is a validation error that carries its own condition reason.
type dependencyError struct {
	reason  string
	message string
}

func (e *dependencyError) Error() string { return e.message }

// validateDependencies checks for missing dependency references and cycles.
func (r *SyncProfileReconciler) validateDependencies(ctx context.Context, profile *stokerv1alpha1.SyncProfile) *dependencyError {
	if len(profile.Spec.DependsOn) == 0 {
		return nil
	}

	// List all profiles in the namespace to build the adjacency map.
	var profileList stokerv1alpha1.SyncProfileList
	if err := r.List(ctx, &profileList, client.InNamespace(profile.Namespace)); err != nil {
		return &dependencyError{
			reason:  conditions.ReasonValidationFailed,
			message: fmt.Sprintf("listing profiles for dependency check: %v", err),
		}
	}

	// Build adjacency map: name → []dependsOn names.
	adj := make(map[string][]string, len(profileList.Items))
	for i := range profileList.Items {
		p := &profileList.Items[i]
		deps := make([]string, len(p.Spec.DependsOn))
		for j, d := range p.Spec.DependsOn {
			deps[j] = d.ProfileName
		}
		adj[p.Name] = deps
	}

	// Check that all dependencies reference existing profiles.
	for _, dep := range profile.Spec.DependsOn {
		if _, exists := adj[dep.ProfileName]; !exists {
			return &dependencyError{
				reason:  conditions.ReasonDependencyNotFound,
				message: fmt.Sprintf("dependency %q not found in namespace %s", dep.ProfileName, profile.Namespace),
			}
		}
	}

	// DFS cycle detection from this profile.
	if cycle := detectCycle(profile.Name, adj); cycle != "" {
		return &dependencyError{
			reason:  conditions.ReasonCycleDetected,
			message: fmt.Sprintf("dependency cycle detected: %s", cycle),
		}
	}

	return nil
}

// detectCycle runs DFS from start and returns a cycle path string if found, or "" if none.
func detectCycle(start string, adj map[string][]string) string {
	const (
		white = 0 // unvisited
		gray  = 1 // in current recursion stack
		black = 2 // fully explored
	)

	color := make(map[string]int)
	parent := make(map[string]string)

	var dfs func(node string) string
	dfs = func(node string) string {
		color[node] = gray
		for _, dep := range adj[node] {
			if color[dep] == gray {
				// Back edge — build cycle path.
				return buildCyclePath(dep, node, parent)
			}
			if color[dep] == white {
				parent[dep] = node
				if cycle := dfs(dep); cycle != "" {
					return cycle
				}
			}
		}
		color[node] = black
		return ""
	}

	return dfs(start)
}

// buildCyclePath reconstructs the cycle from the back-edge target back to itself.
func buildCyclePath(cycleNode, from string, parent map[string]string) string {
	path := []string{cycleNode}
	node := from
	for node != cycleNode {
		path = append([]string{node}, path...)
		node = parent[node]
	}
	path = append([]string{cycleNode}, path...)
	// Reverse to get forward order: cycleNode → ... → from → cycleNode
	// Actually path is already: [cycleNode, ..., from, cycleNode]
	// Let's build it properly.
	return strings.Join(path, " → ")
}

// SetupWithManager sets up the SyncProfile controller with the Manager.
func (r *SyncProfileReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&stokerv1alpha1.SyncProfile{}).
		Named("syncprofile").
		Complete(r)
}
