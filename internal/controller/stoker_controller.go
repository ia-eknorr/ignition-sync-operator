package controller

import (
	"context"
	"fmt"
	"reflect"
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
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	stokerv1alpha1 "github.com/ia-eknorr/stoker-operator/api/v1alpha1"
	"github.com/ia-eknorr/stoker-operator/internal/git"
	"github.com/ia-eknorr/stoker-operator/pkg/conditions"
	stokertypes "github.com/ia-eknorr/stoker-operator/pkg/types"
)

const (
	lsRemoteTimeout = 30 * time.Second
)

// StokerReconciler reconciles an Stoker object.
type StokerReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	GitClient git.Client
	Recorder  record.EventRecorder
}

// +kubebuilder:rbac:groups=stoker.io,resources=stokers,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=stoker.io,resources=stokers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=stoker.io,resources=stokers/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *StokerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Fetch the CR
	var stk stokerv1alpha1.Stoker
	if err := r.Get(ctx, req.NamespacedName, &stk); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Capture the original for merge-patch base (avoids resourceVersion conflicts).
	base := stk.DeepCopy()

	// --- Step 0: Finalizer handling ---

	if !stk.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&stk, stokertypes.Finalizer) {
			log.Info("cleaning up resources for deleted CR")
			if err := r.cleanupOwnedResources(ctx, &stk); err != nil {
				return ctrl.Result{}, err
			}
			controllerutil.RemoveFinalizer(&stk, stokertypes.Finalizer)
			return ctrl.Result{}, r.Update(ctx, &stk)
		}
		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(&stk, stokertypes.Finalizer) {
		controllerutil.AddFinalizer(&stk, stokertypes.Finalizer)
		return ctrl.Result{}, r.Update(ctx, &stk)
	}

	// --- Step 0.5: Check if paused ---

	if stk.Spec.Paused {
		log.Info("CR is paused, skipping reconciliation")
		wasPaused := conditionHasReason(stk.Status.Conditions, conditions.TypeReady, conditions.ReasonPaused)
		r.setCondition(ctx, &stk, conditions.TypeReady, metav1.ConditionFalse, conditions.ReasonPaused, "Reconciliation paused")
		if !wasPaused {
			r.Recorder.Event(&stk, corev1.EventTypeNormal, conditions.ReasonPaused, "Reconciliation paused")
		}
		return ctrl.Result{}, r.patchStatus(ctx, &stk, base)
	}

	// --- Step 1: Validate secrets exist ---

	if err := r.validateSecrets(ctx, &stk); err != nil {
		r.setCondition(ctx, &stk, conditions.TypeReady, metav1.ConditionFalse, conditions.ReasonReconciling, err.Error())
		_ = r.patchStatus(ctx, &stk, base)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, err
	}

	// --- Step 2: Resolve git ref via ls-remote ---

	result, err := r.resolveRef(ctx, &stk)
	if err != nil {
		wasAlreadyFailed := conditionHasStatus(stk.Status.Conditions, conditions.TypeRefResolved, metav1.ConditionFalse)
		r.setCondition(ctx, &stk, conditions.TypeRefResolved, metav1.ConditionFalse, conditions.ReasonRefResolutionFailed, err.Error())
		if !wasAlreadyFailed {
			r.Recorder.Eventf(&stk, corev1.EventTypeWarning, conditions.ReasonRefResolutionFailed, "Ref resolution failed: %s", err.Error())
		}
		stk.Status.RefResolutionStatus = "Error"
		_ = r.patchStatus(ctx, &stk, base)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Ref resolved successfully
	r.setCondition(ctx, &stk, conditions.TypeRefResolved, metav1.ConditionTrue, conditions.ReasonRefResolved, result.Commit)
	stk.Status.RefResolutionStatus = "Resolved"
	stk.Status.LastSyncCommit = result.Commit
	stk.Status.LastSyncRef = result.Ref
	now := metav1.Now()
	stk.Status.LastSyncTime = &now

	// --- Step 3: Create/update metadata ConfigMap ---

	if err := r.ensureMetadataConfigMap(ctx, &stk, result); err != nil {
		log.Error(err, "failed to update metadata ConfigMap")
	}

	// --- Step 4: Discover gateways ---

	prevGatewayCount := len(stk.Status.DiscoveredGateways)
	gateways, err := r.discoverGateways(ctx, &stk)
	if err != nil {
		log.Error(err, "failed to discover gateways")
	} else {
		gateways = r.collectGatewayStatus(ctx, &stk, gateways)
		stk.Status.DiscoveredGateways = gateways

		if len(gateways) != prevGatewayCount {
			r.Recorder.Eventf(&stk, corev1.EventTypeNormal, "GatewaysDiscovered",
				"Discovered %d gateway(s) (was %d)", len(gateways), prevGatewayCount)
		}
	}

	// --- Step 4.5: Update SyncProfile gateway counts ---

	r.updateProfileGatewayCounts(ctx, &stk)

	// --- Step 5: Update conditions ---

	r.updateAllGatewaysSyncedCondition(ctx, &stk)
	r.updateReadyCondition(ctx, &stk)

	// --- Step 6: Update status ---

	stk.Status.ObservedGeneration = stk.Generation
	if err := r.patchStatus(ctx, &stk, base); err != nil {
		return ctrl.Result{}, err
	}

	// --- Step 7: Requeue ---

	requeueAfter := r.pollingInterval(&stk)
	log.Info("reconciliation complete", "commit", result.Commit, "gateways", len(stk.Status.DiscoveredGateways), "requeueAfter", requeueAfter)
	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

// resolveRef resolves the git ref to a commit SHA via ls-remote (single HTTP call, no clone).
func (r *StokerReconciler) resolveRef(ctx context.Context, stk *stokerv1alpha1.Stoker) (git.Result, error) {
	ref := stk.Spec.Git.Ref

	// Check for webhook-requested ref override
	if requested, ok := stk.Annotations[stokertypes.AnnotationRequestedRef]; ok && requested != "" {
		ref = requested
	}

	// If the ref is already resolved at the desired ref and was resolved recently,
	// return cached result to avoid redundant ls-remote calls on status-triggered reconciles.
	if stk.Status.RefResolutionStatus == "Resolved" && stk.Status.LastSyncRef == ref &&
		stk.Status.LastSyncCommit != "" && stk.Status.LastSyncTime != nil {
		sinceLastSync := time.Since(stk.Status.LastSyncTime.Time)
		if sinceLastSync < r.pollingInterval(stk) {
			return git.Result{Commit: stk.Status.LastSyncCommit, Ref: stk.Status.LastSyncRef}, nil
		}
	}

	// Resolve auth and call ls-remote
	auth, err := git.ResolveAuth(ctx, r.Client, stk.Namespace, stk.Spec.Git.Auth)
	if err != nil {
		return git.Result{}, fmt.Errorf("resolving git auth: %w", err)
	}

	lsCtx, cancel := context.WithTimeout(ctx, lsRemoteTimeout)
	defer cancel()

	return r.GitClient.LsRemote(lsCtx, stk.Spec.Git.Repo, ref, auth)
}

// cleanupOwnedResources removes ConfigMaps owned by this CR during deletion.
func (r *StokerReconciler) cleanupOwnedResources(ctx context.Context, stk *stokerv1alpha1.Stoker) error {
	log := logf.FromContext(ctx)

	// Clean up metadata, status, and changes ConfigMaps
	cmNames := []string{
		fmt.Sprintf("stoker-metadata-%s", stk.Name),
		fmt.Sprintf("stoker-status-%s", stk.Name),
		fmt.Sprintf("stoker-changes-%s", stk.Name),
	}

	for _, name := range cmNames {
		cm := &corev1.ConfigMap{}
		err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: stk.Namespace}, cm)
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
func (r *StokerReconciler) validateSecrets(ctx context.Context, stk *stokerv1alpha1.Stoker) error {
	// Gateway API key secret is always required
	secret := &corev1.Secret{}
	key := types.NamespacedName{
		Name:      stk.Spec.Gateway.APIKeySecretRef.Name,
		Namespace: stk.Namespace,
	}
	if err := r.Get(ctx, key, secret); err != nil {
		return fmt.Errorf("gateway API key secret %q not found: %w", key.Name, err)
	}

	// Validate git auth secret if specified
	if stk.Spec.Git.Auth != nil {
		if stk.Spec.Git.Auth.SSHKey != nil {
			key.Name = stk.Spec.Git.Auth.SSHKey.SecretRef.Name
			if err := r.Get(ctx, key, secret); err != nil {
				return fmt.Errorf("SSH key secret %q not found: %w", key.Name, err)
			}
		}
		if stk.Spec.Git.Auth.Token != nil {
			key.Name = stk.Spec.Git.Auth.Token.SecretRef.Name
			if err := r.Get(ctx, key, secret); err != nil {
				return fmt.Errorf("token secret %q not found: %w", key.Name, err)
			}
		}
		if stk.Spec.Git.Auth.GitHubApp != nil {
			key.Name = stk.Spec.Git.Auth.GitHubApp.PrivateKeySecretRef.Name
			if err := r.Get(ctx, key, secret); err != nil {
				return fmt.Errorf("GitHub App private key secret %q not found: %w", key.Name, err)
			}
		}
	}

	return nil
}

// ensureMetadataConfigMap creates or updates the metadata ConfigMap that signals agents.
func (r *StokerReconciler) ensureMetadataConfigMap(ctx context.Context, stk *stokerv1alpha1.Stoker, result git.Result) error {
	cmName := fmt.Sprintf("stoker-metadata-%s", stk.Name)
	cm := &corev1.ConfigMap{}
	key := types.NamespacedName{Name: cmName, Namespace: stk.Namespace}

	data := map[string]string{
		"commit": result.Commit,
		"ref":    result.Ref,
		"gitURL": stk.Spec.Git.Repo,
		"paused": fmt.Sprintf("%t", stk.Spec.Paused),
	}

	// Include auth type so agent knows which credential file to use.
	data["authType"] = resolveAuthType(stk.Spec.Git.Auth)

	// Include exclude patterns as CSV.
	if len(stk.Spec.ExcludePatterns) > 0 {
		data["excludePatterns"] = joinCSV(stk.Spec.ExcludePatterns)
	}

	// Gateway connection info for agent's Ignition API calls.
	data["gatewayPort"] = fmt.Sprintf("%d", stk.Spec.Gateway.Port)
	if stk.Spec.Gateway.TLS != nil {
		data["gatewayTLS"] = fmt.Sprintf("%t", *stk.Spec.Gateway.TLS)
	}

	err := r.Get(ctx, key, cm)
	if errors.IsNotFound(err) {
		cm = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      cmName,
				Namespace: stk.Namespace,
				Labels: map[string]string{
					"app.kubernetes.io/managed-by": "stoker-controller",
					stokertypes.LabelCRName:        stk.Name,
				},
			},
			Data: data,
		}
		if err := controllerutil.SetControllerReference(stk, cm, r.Scheme); err != nil {
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

// joinCSV joins strings with commas.
func joinCSV(items []string) string {
	return strings.Join(items, ",")
}

// setCondition sets a condition on the CR's status.
func (r *StokerReconciler) setCondition(_ context.Context, stk *stokerv1alpha1.Stoker, condType string, status metav1.ConditionStatus, reason, message string) {
	condition := metav1.Condition{
		Type:               condType,
		Status:             status,
		ObservedGeneration: stk.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             reason,
		Message:            message,
	}

	// Replace existing condition of same type, or append
	for i, c := range stk.Status.Conditions {
		if c.Type == condType {
			if c.Status != status {
				stk.Status.Conditions[i] = condition
			} else {
				// Update reason/message but keep transition time
				stk.Status.Conditions[i].Reason = reason
				stk.Status.Conditions[i].Message = message
				stk.Status.Conditions[i].ObservedGeneration = stk.Generation
			}
			return
		}
	}
	stk.Status.Conditions = append(stk.Status.Conditions, condition)
}

// patchStatus applies a status update via server-side merge patch.
// This avoids resourceVersion conflicts when overlapping reconciles both update status.
func (r *StokerReconciler) patchStatus(ctx context.Context, stk *stokerv1alpha1.Stoker, base client.Object) error {
	return r.Status().Patch(ctx, stk, client.MergeFrom(base))
}

// pollingInterval returns the requeue interval from the CR spec.
func (r *StokerReconciler) pollingInterval(stk *stokerv1alpha1.Stoker) time.Duration {
	if stk.Spec.Polling.Enabled != nil && !*stk.Spec.Polling.Enabled {
		return 0 // no requeue if polling disabled
	}
	interval := stk.Spec.Polling.Interval
	if interval == "" {
		interval = "60s"
	}
	d, err := time.ParseDuration(interval)
	if err != nil {
		return 60 * time.Second
	}
	return d
}

// findStokersForProfile returns reconcile requests for all Stoker CRs
// in the same namespace as a changed SyncProfile, so gateway counts can be refreshed.
func (r *StokerReconciler) findStokersForProfile(ctx context.Context, obj client.Object) []reconcile.Request {
	var stkList stokerv1alpha1.StokerList
	if err := r.List(ctx, &stkList, client.InNamespace(obj.GetNamespace())); err != nil {
		return nil
	}

	requests := make([]reconcile.Request, 0, len(stkList.Items))
	for _, stk := range stkList.Items {
		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      stk.Name,
				Namespace: stk.Namespace,
			},
		})
	}
	return requests
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
func (r *StokerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&stokerv1alpha1.Stoker{}, builder.WithPredicates(annotationOrGenerationChanged{})).
		Owns(&corev1.ConfigMap{}).
		Watches(&corev1.Pod{}, handler.EnqueueRequestsFromMapFunc(r.findStokerForPod)).
		Watches(&stokerv1alpha1.SyncProfile{}, handler.EnqueueRequestsFromMapFunc(r.findStokersForProfile)).
		WithOptions(controller.Options{MaxConcurrentReconciles: 5}).
		Named("stoker").
		Complete(r)
}
