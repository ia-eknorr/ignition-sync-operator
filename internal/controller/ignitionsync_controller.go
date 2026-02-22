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

	syncv1alpha1 "github.com/ia-eknorr/ignition-sync-operator/api/v1alpha1"
	"github.com/ia-eknorr/ignition-sync-operator/internal/git"
	"github.com/ia-eknorr/ignition-sync-operator/pkg/conditions"
	synctypes "github.com/ia-eknorr/ignition-sync-operator/pkg/types"
)

const (
	lsRemoteTimeout = 30 * time.Second
)

// IgnitionSyncReconciler reconciles an IgnitionSync object.
type IgnitionSyncReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	GitClient git.Client
	Recorder  record.EventRecorder
}

// +kubebuilder:rbac:groups=sync.ignition.io,resources=ignitionsyncs,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=sync.ignition.io,resources=ignitionsyncs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=sync.ignition.io,resources=ignitionsyncs/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *IgnitionSyncReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Fetch the CR
	var isync syncv1alpha1.IgnitionSync
	if err := r.Get(ctx, req.NamespacedName, &isync); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Capture the original for merge-patch base (avoids resourceVersion conflicts).
	base := isync.DeepCopy()

	// --- Step 0: Finalizer handling ---

	if !isync.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&isync, synctypes.Finalizer) {
			log.Info("cleaning up resources for deleted CR")
			if err := r.cleanupOwnedResources(ctx, &isync); err != nil {
				return ctrl.Result{}, err
			}
			controllerutil.RemoveFinalizer(&isync, synctypes.Finalizer)
			return ctrl.Result{}, r.Update(ctx, &isync)
		}
		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(&isync, synctypes.Finalizer) {
		controllerutil.AddFinalizer(&isync, synctypes.Finalizer)
		return ctrl.Result{}, r.Update(ctx, &isync)
	}

	// --- Step 0.5: Check if paused ---

	if isync.Spec.Paused {
		log.Info("CR is paused, skipping reconciliation")
		r.setCondition(ctx, &isync, conditions.TypeReady, metav1.ConditionFalse, conditions.ReasonPaused, "Reconciliation paused")
		return ctrl.Result{}, r.patchStatus(ctx, &isync, base)
	}

	// --- Step 1: Validate secrets exist ---

	if err := r.validateSecrets(ctx, &isync); err != nil {
		r.setCondition(ctx, &isync, conditions.TypeReady, metav1.ConditionFalse, conditions.ReasonReconciling, err.Error())
		_ = r.patchStatus(ctx, &isync, base)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, err
	}

	// --- Step 2: Resolve git ref via ls-remote ---

	result, err := r.resolveRef(ctx, &isync)
	if err != nil {
		r.setCondition(ctx, &isync, conditions.TypeRefResolved, metav1.ConditionFalse, conditions.ReasonRefResolutionFailed, err.Error())
		isync.Status.RefResolutionStatus = "Error"
		_ = r.patchStatus(ctx, &isync, base)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Ref resolved successfully
	r.setCondition(ctx, &isync, conditions.TypeRefResolved, metav1.ConditionTrue, conditions.ReasonRefResolved, result.Commit)
	isync.Status.RefResolutionStatus = "Resolved"
	isync.Status.LastSyncCommit = result.Commit
	isync.Status.LastSyncRef = result.Ref
	now := metav1.Now()
	isync.Status.LastSyncTime = &now

	// --- Step 3: Create/update metadata ConfigMap ---

	if err := r.ensureMetadataConfigMap(ctx, &isync, result); err != nil {
		log.Error(err, "failed to update metadata ConfigMap")
	}

	// --- Step 4: Discover gateways ---

	prevGatewayCount := len(isync.Status.DiscoveredGateways)
	gateways, err := r.discoverGateways(ctx, &isync)
	if err != nil {
		log.Error(err, "failed to discover gateways")
	} else {
		gateways = r.collectGatewayStatus(ctx, &isync, gateways)
		isync.Status.DiscoveredGateways = gateways

		if len(gateways) != prevGatewayCount {
			r.Recorder.Eventf(&isync, corev1.EventTypeNormal, "GatewaysDiscovered",
				"Discovered %d gateway(s) (was %d)", len(gateways), prevGatewayCount)
		}
	}

	// --- Step 4.5: Update SyncProfile gateway counts ---

	r.updateProfileGatewayCounts(ctx, &isync)

	// --- Step 5: Update conditions ---

	r.updateAllGatewaysSyncedCondition(ctx, &isync)
	r.updateReadyCondition(ctx, &isync)

	// --- Step 6: Update status ---

	isync.Status.ObservedGeneration = isync.Generation
	if err := r.patchStatus(ctx, &isync, base); err != nil {
		return ctrl.Result{}, err
	}

	// --- Step 7: Requeue ---

	requeueAfter := r.pollingInterval(&isync)
	log.Info("reconciliation complete", "commit", result.Commit, "gateways", len(isync.Status.DiscoveredGateways), "requeueAfter", requeueAfter)
	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

// resolveRef resolves the git ref to a commit SHA via ls-remote (single HTTP call, no clone).
func (r *IgnitionSyncReconciler) resolveRef(ctx context.Context, isync *syncv1alpha1.IgnitionSync) (git.Result, error) {
	ref := isync.Spec.Git.Ref

	// Check for webhook-requested ref override
	if requested, ok := isync.Annotations[synctypes.AnnotationRequestedRef]; ok && requested != "" {
		ref = requested
	}

	// If the ref is already resolved at the desired ref and was resolved recently,
	// return cached result to avoid redundant ls-remote calls on status-triggered reconciles.
	if isync.Status.RefResolutionStatus == "Resolved" && isync.Status.LastSyncRef == ref &&
		isync.Status.LastSyncCommit != "" && isync.Status.LastSyncTime != nil {
		sinceLastSync := time.Since(isync.Status.LastSyncTime.Time)
		if sinceLastSync < r.pollingInterval(isync) {
			return git.Result{Commit: isync.Status.LastSyncCommit, Ref: isync.Status.LastSyncRef}, nil
		}
	}

	// Resolve auth and call ls-remote
	auth, err := git.ResolveAuth(ctx, r.Client, isync.Namespace, isync.Spec.Git.Auth)
	if err != nil {
		return git.Result{}, fmt.Errorf("resolving git auth: %w", err)
	}

	lsCtx, cancel := context.WithTimeout(ctx, lsRemoteTimeout)
	defer cancel()

	return r.GitClient.LsRemote(lsCtx, isync.Spec.Git.Repo, ref, auth)
}

// cleanupOwnedResources removes ConfigMaps owned by this CR during deletion.
func (r *IgnitionSyncReconciler) cleanupOwnedResources(ctx context.Context, isync *syncv1alpha1.IgnitionSync) error {
	log := logf.FromContext(ctx)

	// Clean up metadata, status, and changes ConfigMaps
	cmNames := []string{
		fmt.Sprintf("ignition-sync-metadata-%s", isync.Name),
		fmt.Sprintf("ignition-sync-status-%s", isync.Name),
		fmt.Sprintf("ignition-sync-changes-%s", isync.Name),
	}

	for _, name := range cmNames {
		cm := &corev1.ConfigMap{}
		err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: isync.Namespace}, cm)
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
func (r *IgnitionSyncReconciler) validateSecrets(ctx context.Context, isync *syncv1alpha1.IgnitionSync) error {
	// Gateway API key secret is always required
	secret := &corev1.Secret{}
	key := types.NamespacedName{
		Name:      isync.Spec.Gateway.APIKeySecretRef.Name,
		Namespace: isync.Namespace,
	}
	if err := r.Get(ctx, key, secret); err != nil {
		return fmt.Errorf("gateway API key secret %q not found: %w", key.Name, err)
	}

	// Validate git auth secret if specified
	if isync.Spec.Git.Auth != nil {
		if isync.Spec.Git.Auth.SSHKey != nil {
			key.Name = isync.Spec.Git.Auth.SSHKey.SecretRef.Name
			if err := r.Get(ctx, key, secret); err != nil {
				return fmt.Errorf("SSH key secret %q not found: %w", key.Name, err)
			}
		}
		if isync.Spec.Git.Auth.Token != nil {
			key.Name = isync.Spec.Git.Auth.Token.SecretRef.Name
			if err := r.Get(ctx, key, secret); err != nil {
				return fmt.Errorf("token secret %q not found: %w", key.Name, err)
			}
		}
		if isync.Spec.Git.Auth.GitHubApp != nil {
			key.Name = isync.Spec.Git.Auth.GitHubApp.PrivateKeySecretRef.Name
			if err := r.Get(ctx, key, secret); err != nil {
				return fmt.Errorf("GitHub App private key secret %q not found: %w", key.Name, err)
			}
		}
	}

	return nil
}

// ensureMetadataConfigMap creates or updates the metadata ConfigMap that signals agents.
func (r *IgnitionSyncReconciler) ensureMetadataConfigMap(ctx context.Context, isync *syncv1alpha1.IgnitionSync, result git.Result) error {
	cmName := fmt.Sprintf("ignition-sync-metadata-%s", isync.Name)
	cm := &corev1.ConfigMap{}
	key := types.NamespacedName{Name: cmName, Namespace: isync.Namespace}

	data := map[string]string{
		"commit": result.Commit,
		"ref":    result.Ref,
		"gitURL": isync.Spec.Git.Repo,
		"paused": fmt.Sprintf("%t", isync.Spec.Paused),
	}

	// Include auth type so agent knows which credential file to use.
	data["authType"] = resolveAuthType(isync.Spec.Git.Auth)

	// Include exclude patterns as CSV.
	if len(isync.Spec.ExcludePatterns) > 0 {
		data["excludePatterns"] = joinCSV(isync.Spec.ExcludePatterns)
	}

	// Gateway connection info for agent's Ignition API calls.
	data["gatewayPort"] = fmt.Sprintf("%d", isync.Spec.Gateway.Port)
	if isync.Spec.Gateway.TLS != nil {
		data["gatewayTLS"] = fmt.Sprintf("%t", *isync.Spec.Gateway.TLS)
	}

	err := r.Get(ctx, key, cm)
	if errors.IsNotFound(err) {
		cm = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      cmName,
				Namespace: isync.Namespace,
				Labels: map[string]string{
					"app.kubernetes.io/managed-by": "ignition-sync-controller",
					synctypes.LabelCRName:          isync.Name,
				},
			},
			Data: data,
		}
		if err := controllerutil.SetControllerReference(isync, cm, r.Scheme); err != nil {
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
func resolveAuthType(auth *syncv1alpha1.GitAuthSpec) string {
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
func (r *IgnitionSyncReconciler) setCondition(_ context.Context, isync *syncv1alpha1.IgnitionSync, condType string, status metav1.ConditionStatus, reason, message string) {
	condition := metav1.Condition{
		Type:               condType,
		Status:             status,
		ObservedGeneration: isync.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             reason,
		Message:            message,
	}

	// Replace existing condition of same type, or append
	for i, c := range isync.Status.Conditions {
		if c.Type == condType {
			if c.Status != status {
				isync.Status.Conditions[i] = condition
			} else {
				// Update reason/message but keep transition time
				isync.Status.Conditions[i].Reason = reason
				isync.Status.Conditions[i].Message = message
				isync.Status.Conditions[i].ObservedGeneration = isync.Generation
			}
			return
		}
	}
	isync.Status.Conditions = append(isync.Status.Conditions, condition)
}

// patchStatus applies a status update via server-side merge patch.
// This avoids resourceVersion conflicts when overlapping reconciles both update status.
func (r *IgnitionSyncReconciler) patchStatus(ctx context.Context, isync *syncv1alpha1.IgnitionSync, base client.Object) error {
	return r.Status().Patch(ctx, isync, client.MergeFrom(base))
}

// pollingInterval returns the requeue interval from the CR spec.
func (r *IgnitionSyncReconciler) pollingInterval(isync *syncv1alpha1.IgnitionSync) time.Duration {
	if isync.Spec.Polling.Enabled != nil && !*isync.Spec.Polling.Enabled {
		return 0 // no requeue if polling disabled
	}
	interval := isync.Spec.Polling.Interval
	if interval == "" {
		interval = "60s"
	}
	d, err := time.ParseDuration(interval)
	if err != nil {
		return 60 * time.Second
	}
	return d
}

// findIgnitionSyncsForProfile returns reconcile requests for all IgnitionSync CRs
// in the same namespace as a changed SyncProfile, so gateway counts can be refreshed.
func (r *IgnitionSyncReconciler) findIgnitionSyncsForProfile(ctx context.Context, obj client.Object) []reconcile.Request {
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
func (r *IgnitionSyncReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&syncv1alpha1.IgnitionSync{}, builder.WithPredicates(annotationOrGenerationChanged{})).
		Owns(&corev1.ConfigMap{}).
		Watches(&corev1.Pod{}, handler.EnqueueRequestsFromMapFunc(r.findIgnitionSyncForPod)).
		Watches(&syncv1alpha1.SyncProfile{}, handler.EnqueueRequestsFromMapFunc(r.findIgnitionSyncsForProfile)).
		WithOptions(controller.Options{MaxConcurrentReconciles: 5}).
		Named("ignitionsync").
		Complete(r)
}
