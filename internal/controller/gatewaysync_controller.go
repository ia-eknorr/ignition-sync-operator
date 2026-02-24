package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	stokerv1alpha1 "github.com/ia-eknorr/stoker-operator/api/v1alpha1"
	"github.com/ia-eknorr/stoker-operator/internal/git"
	"github.com/ia-eknorr/stoker-operator/pkg/conditions"
	stokertypes "github.com/ia-eknorr/stoker-operator/pkg/types"
)

const (
	lsRemoteTimeout = 30 * time.Second
)

// GatewaySyncReconciler reconciles a GatewaySync object.
type GatewaySyncReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	GitClient git.Client
	Recorder  record.EventRecorder
}

// +kubebuilder:rbac:groups=stoker.io,resources=gatewaysyncs,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=stoker.io,resources=gatewaysyncs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=stoker.io,resources=gatewaysyncs/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *GatewaySyncReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Fetch the CR
	var gs stokerv1alpha1.GatewaySync
	if err := r.Get(ctx, req.NamespacedName, &gs); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Capture the original for merge-patch base (avoids resourceVersion conflicts).
	base := gs.DeepCopy()

	// --- Step 0: Finalizer handling ---

	if !gs.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&gs, stokertypes.Finalizer) {
			log.Info("cleaning up resources for deleted CR")
			if err := r.cleanupOwnedResources(ctx, &gs); err != nil {
				return ctrl.Result{}, err
			}
			controllerutil.RemoveFinalizer(&gs, stokertypes.Finalizer)
			return ctrl.Result{}, r.Update(ctx, &gs)
		}
		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(&gs, stokertypes.Finalizer) {
		controllerutil.AddFinalizer(&gs, stokertypes.Finalizer)
		return ctrl.Result{}, r.Update(ctx, &gs)
	}

	// --- Step 0.5: Check if paused ---

	if gs.Spec.Paused {
		log.Info("CR is paused, skipping reconciliation")
		wasPaused := conditionHasReason(gs.Status.Conditions, conditions.TypeReady, conditions.ReasonPaused)
		r.setCondition(ctx, &gs, conditions.TypeReady, metav1.ConditionFalse, conditions.ReasonPaused, "Reconciliation paused")
		if !wasPaused {
			r.Recorder.Event(&gs, corev1.EventTypeNormal, conditions.ReasonPaused, "Reconciliation paused")
		}
		return ctrl.Result{}, r.patchStatus(ctx, &gs, base)
	}

	// --- Step 1: Validate profiles ---

	if err := r.validateProfiles(&gs); err != nil {
		r.setCondition(ctx, &gs, conditions.TypeProfilesValid, metav1.ConditionFalse, conditions.ReasonProfilesInvalid, err.Error())
		r.Recorder.Eventf(&gs, corev1.EventTypeWarning, conditions.ReasonProfilesInvalid, "Profile validation failed: %s", err.Error())
	} else {
		r.setCondition(ctx, &gs, conditions.TypeProfilesValid, metav1.ConditionTrue, conditions.ReasonProfilesValid, "All profiles valid")
	}

	// --- Step 2: Validate secrets exist ---

	if err := r.validateSecrets(ctx, &gs); err != nil {
		r.setCondition(ctx, &gs, conditions.TypeReady, metav1.ConditionFalse, conditions.ReasonReconciling, err.Error())
		_ = r.patchStatus(ctx, &gs, base)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, err
	}

	// --- Step 3: Resolve git ref via ls-remote ---

	result, err := r.resolveRef(ctx, &gs)
	if err != nil {
		wasAlreadyFailed := conditionHasStatus(gs.Status.Conditions, conditions.TypeRefResolved, metav1.ConditionFalse)
		r.setCondition(ctx, &gs, conditions.TypeRefResolved, metav1.ConditionFalse, conditions.ReasonRefResolutionFailed, err.Error())
		if !wasAlreadyFailed {
			r.Recorder.Eventf(&gs, corev1.EventTypeWarning, conditions.ReasonRefResolutionFailed, "Ref resolution failed: %s", err.Error())
		}
		gs.Status.RefResolutionStatus = "Error"
		_ = r.patchStatus(ctx, &gs, base)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Ref resolved successfully
	r.setCondition(ctx, &gs, conditions.TypeRefResolved, metav1.ConditionTrue, conditions.ReasonRefResolved, result.Commit)
	gs.Status.RefResolutionStatus = "Resolved"
	if gs.Status.LastSyncCommit != result.Commit {
		gs.Status.LastSyncCommit = result.Commit
		gs.Status.LastSyncCommitShort = shortCommit(result.Commit)
		gs.Status.LastSyncRef = result.Ref
		now := metav1.Now()
		gs.Status.LastSyncTime = &now
	}

	// --- Step 4: Create/update metadata ConfigMap ---

	if err := r.ensureMetadataConfigMap(ctx, &gs, result); err != nil {
		log.Error(err, "failed to update metadata ConfigMap")
	}

	// --- Step 5: Discover gateways ---

	prevGatewayCount := len(gs.Status.DiscoveredGateways)
	gateways, err := r.discoverGateways(ctx, &gs)
	if err != nil {
		log.Error(err, "failed to discover gateways")
	} else {
		gateways = r.collectGatewayStatus(ctx, &gs, gateways)
		gs.Status.DiscoveredGateways = gateways

		if len(gateways) != prevGatewayCount {
			r.Recorder.Eventf(&gs, corev1.EventTypeNormal, "GatewaysDiscovered",
				"Discovered %d gateway(s) (was %d)", len(gateways), prevGatewayCount)
		}
	}

	// --- Step 6: Update conditions ---

	r.updateAllGatewaysSyncedCondition(ctx, &gs)
	r.updateReadyCondition(ctx, &gs)

	// --- Step 7: Update status ---

	gs.Status.ObservedGeneration = gs.Generation
	gs.Status.ProfileCount = int32(len(gs.Spec.Sync.Profiles))
	if err := r.patchStatus(ctx, &gs, base); err != nil {
		return ctrl.Result{}, err
	}

	// --- Step 8: Requeue ---

	requeueAfter := r.pollingInterval(&gs)
	log.Info("reconciliation complete", "commit", result.Commit, "gateways", len(gs.Status.DiscoveredGateways), "requeueAfter", requeueAfter)
	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

// validateProfiles validates all embedded profiles for path safety.
func (r *GatewaySyncReconciler) validateProfiles(gs *stokerv1alpha1.GatewaySync) error {
	for name, profile := range gs.Spec.Sync.Profiles {
		for i, m := range profile.Mappings {
			if err := validatePath(m.Source, fmt.Sprintf("profiles[%s].mappings[%d].source", name, i)); err != nil {
				return err
			}
			if err := validatePath(m.Destination, fmt.Sprintf("profiles[%s].mappings[%d].destination", name, i)); err != nil {
				return err
			}
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

// resolveProfiles merges defaults into each profile, returning fully-resolved profiles.
func (r *GatewaySyncReconciler) resolveProfiles(gs *stokerv1alpha1.GatewaySync) map[string]stokertypes.ResolvedProfile {
	defaults := gs.Spec.Sync.Defaults
	resolved := make(map[string]stokertypes.ResolvedProfile, len(gs.Spec.Sync.Profiles))

	for name, p := range gs.Spec.Sync.Profiles {
		rp := stokertypes.ResolvedProfile{
			Vars: p.Vars,
		}

		// Resolve mappings
		rp.Mappings = make([]stokertypes.ResolvedMapping, len(p.Mappings))
		for i, m := range p.Mappings {
			rp.Mappings[i] = stokertypes.ResolvedMapping{
				Source:      m.Source,
				Destination: m.Destination,
				Type:        m.Type,
				Required:    m.Required,
			}
			if rp.Mappings[i].Type == "" {
				rp.Mappings[i].Type = "dir"
			}
		}

		// Merge excludes: defaults + profile-specific
		rp.ExcludePatterns = append([]string{}, defaults.ExcludePatterns...)
		rp.ExcludePatterns = append(rp.ExcludePatterns, p.ExcludePatterns...)

		// Apply overrides with nil-means-inherit
		rp.SyncPeriod = defaults.SyncPeriod
		if p.SyncPeriod != nil {
			rp.SyncPeriod = *p.SyncPeriod
		}
		if rp.SyncPeriod == 0 {
			rp.SyncPeriod = 30
		}

		rp.DryRun = defaults.DryRun
		if p.DryRun != nil {
			rp.DryRun = *p.DryRun
		}

		rp.DesignerSessionPolicy = defaults.DesignerSessionPolicy
		if p.DesignerSessionPolicy != "" {
			rp.DesignerSessionPolicy = p.DesignerSessionPolicy
		}
		if rp.DesignerSessionPolicy == "" {
			rp.DesignerSessionPolicy = "proceed"
		}

		rp.Paused = defaults.Paused
		if p.Paused != nil {
			rp.Paused = *p.Paused
		}

		resolved[name] = rp
	}

	return resolved
}

// resolveRef resolves the git ref to a commit SHA via ls-remote (single HTTP call, no clone).
func (r *GatewaySyncReconciler) resolveRef(ctx context.Context, gs *stokerv1alpha1.GatewaySync) (git.Result, error) {
	ref := gs.Spec.Git.Ref

	// Check for webhook-requested ref override
	if requested, ok := gs.Annotations[stokertypes.AnnotationRequestedRef]; ok && requested != "" {
		ref = requested
	}

	// If the ref is already resolved at the desired ref and was resolved recently,
	// return cached result to avoid redundant ls-remote calls on status-triggered reconciles.
	if gs.Status.RefResolutionStatus == "Resolved" && gs.Status.LastSyncRef == ref &&
		gs.Status.LastSyncCommit != "" && gs.Status.LastSyncTime != nil {
		sinceLastSync := time.Since(gs.Status.LastSyncTime.Time)
		if sinceLastSync < r.pollingInterval(gs) {
			return git.Result{Commit: gs.Status.LastSyncCommit, Ref: gs.Status.LastSyncRef}, nil
		}
	}

	// Resolve auth and call ls-remote
	auth, err := git.ResolveAuth(ctx, r.Client, gs.Namespace, gs.Spec.Git.Auth)
	if err != nil {
		return git.Result{}, fmt.Errorf("resolving git auth: %w", err)
	}

	lsCtx, cancel := context.WithTimeout(ctx, lsRemoteTimeout)
	defer cancel()

	return r.GitClient.LsRemote(lsCtx, gs.Spec.Git.Repo, ref, auth)
}

// cleanupOwnedResources removes ConfigMaps owned by this CR during deletion.
func (r *GatewaySyncReconciler) cleanupOwnedResources(ctx context.Context, gs *stokerv1alpha1.GatewaySync) error {
	log := logf.FromContext(ctx)

	// Clean up metadata, status, and changes ConfigMaps
	cmNames := []string{
		fmt.Sprintf("stoker-metadata-%s", gs.Name),
		fmt.Sprintf("stoker-status-%s", gs.Name),
		fmt.Sprintf("stoker-changes-%s", gs.Name),
	}

	for _, name := range cmNames {
		cm := &corev1.ConfigMap{}
		err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: gs.Namespace}, cm)
		if errors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("getting ConfigMap %s: %w", name, err)
		}
		if err := r.Delete(ctx, cm); err != nil && !errors.IsNotFound(err) {
			return fmt.Errorf("deleting ConfigMap %s: %w", name, err)
		}
		log.Info("deleted ConfigMap", "name", name)
	}

	return nil
}

// validateSecrets checks that referenced secrets exist.
func (r *GatewaySyncReconciler) validateSecrets(ctx context.Context, gs *stokerv1alpha1.GatewaySync) error {
	// Gateway API key secret is always required
	secret := &corev1.Secret{}
	key := types.NamespacedName{
		Name:      gs.Spec.Gateway.APIKeySecretRef.Name,
		Namespace: gs.Namespace,
	}
	if err := r.Get(ctx, key, secret); err != nil {
		return fmt.Errorf("gateway API key secret %q not found: %w", key.Name, err)
	}

	// Validate git auth secret if specified
	if gs.Spec.Git.Auth != nil {
		if gs.Spec.Git.Auth.SSHKey != nil {
			key.Name = gs.Spec.Git.Auth.SSHKey.SecretRef.Name
			if err := r.Get(ctx, key, secret); err != nil {
				return fmt.Errorf("SSH key secret %q not found: %w", key.Name, err)
			}
		}
		if gs.Spec.Git.Auth.Token != nil {
			key.Name = gs.Spec.Git.Auth.Token.SecretRef.Name
			if err := r.Get(ctx, key, secret); err != nil {
				return fmt.Errorf("token secret %q not found: %w", key.Name, err)
			}
		}
		if gs.Spec.Git.Auth.GitHubApp != nil {
			key.Name = gs.Spec.Git.Auth.GitHubApp.PrivateKeySecretRef.Name
			if err := r.Get(ctx, key, secret); err != nil {
				return fmt.Errorf("GitHub App private key secret %q not found: %w", key.Name, err)
			}
		}
	}

	return nil
}

// shortCommit returns the first 7 characters of a commit SHA, or the full string if shorter.
func shortCommit(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// ensureMetadataConfigMap creates or updates the metadata ConfigMap that signals agents.
func (r *GatewaySyncReconciler) ensureMetadataConfigMap(ctx context.Context, gs *stokerv1alpha1.GatewaySync, result git.Result) error {
	cmName := fmt.Sprintf("stoker-metadata-%s", gs.Name)
	cm := &corev1.ConfigMap{}
	key := types.NamespacedName{Name: cmName, Namespace: gs.Namespace}

	data := map[string]string{
		"commit": result.Commit,
		"ref":    result.Ref,
		"gitURL": gs.Spec.Git.Repo,
		"paused": fmt.Sprintf("%t", gs.Spec.Paused),
	}

	// Include auth type so agent knows which credential file to use.
	data["authType"] = resolveAuthType(gs.Spec.Git.Auth)

	// Serialize resolved profiles as JSON.
	profiles := r.resolveProfiles(gs)
	profilesJSON, err := json.Marshal(profiles)
	if err != nil {
		return fmt.Errorf("serializing profiles: %w", err)
	}
	data["profiles"] = string(profilesJSON)

	// Gateway connection info for agent's Ignition API calls.
	data["gatewayPort"] = fmt.Sprintf("%d", gs.Spec.Gateway.Port)
	if gs.Spec.Gateway.TLS != nil {
		data["gatewayTLS"] = fmt.Sprintf("%t", *gs.Spec.Gateway.TLS)
	}

	err = r.Get(ctx, key, cm)
	if errors.IsNotFound(err) {
		cm = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      cmName,
				Namespace: gs.Namespace,
				Labels: map[string]string{
					"app.kubernetes.io/managed-by": "stoker-controller",
					stokertypes.LabelCRName:        gs.Name,
				},
			},
			Data: data,
		}
		if err := controllerutil.SetControllerReference(gs, cm, r.Scheme); err != nil {
			return fmt.Errorf("setting owner ref on ConfigMap: %w", err)
		}
		return r.Create(ctx, cm)
	}
	if err != nil {
		return fmt.Errorf("getting ConfigMap %s: %w", cmName, err)
	}

	if reflect.DeepEqual(cm.Data, data) {
		return nil
	}
	cm.Data = data
	return r.Update(ctx, cm)
}

// resolveAuthType determines the auth type string from the git auth spec.
func resolveAuthType(auth *stokerv1alpha1.GitAuthSpec) string {
	if auth == nil {
		return "none"
	}
	if auth.SSHKey != nil {
		return "ssh"
	}
	if auth.Token != nil {
		return "token"
	}
	if auth.GitHubApp != nil {
		return "githubApp"
	}
	return "none"
}

// setCondition sets a condition on the CR's status.
func (r *GatewaySyncReconciler) setCondition(_ context.Context, gs *stokerv1alpha1.GatewaySync, condType string, status metav1.ConditionStatus, reason, message string) {
	condition := metav1.Condition{
		Type:               condType,
		Status:             status,
		ObservedGeneration: gs.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             reason,
		Message:            message,
	}

	// Replace existing condition of same type, or append
	for i, c := range gs.Status.Conditions {
		if c.Type == condType {
			if c.Status != status {
				gs.Status.Conditions[i] = condition
			} else {
				// Update reason/message but keep transition time
				gs.Status.Conditions[i].Reason = reason
				gs.Status.Conditions[i].Message = message
				gs.Status.Conditions[i].ObservedGeneration = gs.Generation
			}
			return
		}
	}
	gs.Status.Conditions = append(gs.Status.Conditions, condition)
}

// patchStatus applies a status update via server-side merge patch.
// This avoids resourceVersion conflicts when overlapping reconciles both update status.
func (r *GatewaySyncReconciler) patchStatus(ctx context.Context, gs *stokerv1alpha1.GatewaySync, base client.Object) error {
	return r.Status().Patch(ctx, gs, client.MergeFrom(base))
}

// pollingInterval returns the requeue interval from the CR spec.
func (r *GatewaySyncReconciler) pollingInterval(gs *stokerv1alpha1.GatewaySync) time.Duration {
	if gs.Spec.Polling.Enabled != nil && !*gs.Spec.Polling.Enabled {
		return 0 // no requeue if polling disabled
	}
	interval := gs.Spec.Polling.Interval
	if interval == "" {
		interval = "60s"
	}
	d, err := time.ParseDuration(interval)
	if err != nil {
		return 60 * time.Second
	}
	return d
}

// conditionHasStatus returns true if the conditions slice already contains
// a condition of the given type with the given status.
func conditionHasStatus(conds []metav1.Condition, condType string, status metav1.ConditionStatus) bool {
	for _, c := range conds {
		if c.Type == condType && c.Status == status {
			return true
		}
	}
	return false
}

// conditionHasReason returns true if the conditions slice already contains
// a condition of the given type with the given reason.
func conditionHasReason(conds []metav1.Condition, condType, reason string) bool {
	for _, c := range conds {
		if c.Type == condType && c.Reason == reason {
			return true
		}
	}
	return false
}

// annotationOrGenerationChanged passes update events where either the
// generation changed (spec edits) or annotations changed (webhook receiver).
// This filters out status-only patches that would cause reconcile noise.
type annotationOrGenerationChanged struct {
	predicate.GenerationChangedPredicate
}

func (p annotationOrGenerationChanged) Update(e event.UpdateEvent) bool {
	if p.GenerationChangedPredicate.Update(e) {
		return true
	}
	return !reflect.DeepEqual(e.ObjectOld.GetAnnotations(), e.ObjectNew.GetAnnotations())
}

// SetupWithManager sets up the controller with the Manager.
func (r *GatewaySyncReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&stokerv1alpha1.GatewaySync{}, builder.WithPredicates(annotationOrGenerationChanged{})).
		Owns(&corev1.ConfigMap{}).
		Watches(&corev1.Pod{}, handler.EnqueueRequestsFromMapFunc(r.findGatewaySyncForPod)).
		WithOptions(controller.Options{MaxConcurrentReconciles: 5}).
		Named("gatewaysync").
		Complete(r)
}
