package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-git/go-git/v5/plumbing/transport"
	gogithttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	gogitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"golang.org/x/crypto/ssh"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	stokerv1alpha1 "github.com/ia-eknorr/stoker-operator/api/v1alpha1"
	"github.com/ia-eknorr/stoker-operator/internal/git"
	"github.com/ia-eknorr/stoker-operator/internal/ignition"
	"github.com/ia-eknorr/stoker-operator/internal/syncengine"
	"github.com/ia-eknorr/stoker-operator/pkg/conditions"
	stokertypes "github.com/ia-eknorr/stoker-operator/pkg/types"
)

const agentVersion = "0.1.0"

// Agent orchestrates the sync process.
type Agent struct {
	Config       *Config
	K8sClient    client.Client
	GitClient    git.Client
	SyncEngine   *syncengine.Engine
	IgnitionAPI  *ignition.Client
	HealthServer *HealthServer
	Watcher      *Watcher
	Recorder     record.EventRecorder // may be nil

	stkRef           *stokerv1alpha1.Stoker // cached for event target
	lastSyncedCommit string
	initialSyncDone  bool
}

// New creates a new Agent with all dependencies wired.
func New(cfg *Config, k8sClient client.Client, recorder record.EventRecorder) *Agent {
	// Build exclude patterns.
	excludes := []string{"**/.git/**", "**/.git", "**/.gitkeep", "**/.resources/**", "**/.resources"}

	// Build Ignition API client.
	igClient := ignition.NewClient(cfg.GatewayScheme(), cfg.GatewayHost(), cfg.APIKey())

	return &Agent{
		Config:       cfg,
		K8sClient:    k8sClient,
		GitClient:    &git.GoGitClient{},
		SyncEngine:   &syncengine.Engine{ExcludePatterns: excludes},
		IgnitionAPI:  igClient,
		HealthServer: NewHealthServer(":8082"),
		Watcher:      NewWatcher(k8sClient, cfg.CRNamespace, cfg.CRName, time.Duration(cfg.SyncPeriod)*time.Second),
		Recorder:     recorder,
	}
}

// Run starts the agent. It clones the repo, performs the initial sync, marks
// the startup probe as ready (which gates the gateway container start when
// deployed as a native sidecar), then watches for changes until ctx is cancelled.
func (a *Agent) Run(ctx context.Context) error {
	log := logf.FromContext(ctx).WithName("agent")

	log.Info("starting agent",
		"gateway", a.Config.GatewayName,
		"cr", a.Config.CRName,
		"namespace", a.Config.CRNamespace,
		"repoPath", a.Config.RepoPath,
		"dataPath", a.Config.DataPath,
		"syncPeriod", a.Config.SyncPeriod,
	)

	// Start health server immediately so startup probe has an endpoint.
	go a.HealthServer.Start(ctx)

	// Read metadata ConfigMap to get git URL and commit.
	log.Info("reading metadata ConfigMap")
	meta, err := a.waitForMetadata(ctx)
	if err != nil {
		return fmt.Errorf("waiting for metadata: %w", err)
	}

	log.Info("metadata loaded", "gitURL", meta.GitURL, "commit", meta.Commit, "ref", meta.Ref)

	// Cache Stoker CR reference for event emission.
	a.fetchISyncRef(ctx)

	// Resolve git auth from mounted files.
	auth := a.resolveFileAuth()

	// Use git URL from metadata ConfigMap, fall back to empty (shouldn't happen).
	gitURL := meta.GitURL
	if gitURL == "" {
		return fmt.Errorf("gitURL not found in metadata ConfigMap")
	}

	// Initial clone.
	log.Info("cloning repository", "url", gitURL, "ref", meta.Ref)
	result, err := a.GitClient.CloneOrFetch(ctx, gitURL, meta.Ref, a.Config.RepoPath, auth)
	if err != nil {
		a.event(corev1.EventTypeWarning, conditions.ReasonCloneFailed, "Initial clone failed: %v", err)
		return fmt.Errorf("initial clone: %w", err)
	}
	log.Info("clone complete", "commit", result.Commit)

	// Initial sync (blocking). Files land on disk before startup probe passes,
	// so the gateway container won't start until config is ready.
	log.Info("performing initial sync")
	syncErr := a.syncOnce(ctx, result.Commit, result.Ref, true)
	if syncErr != nil {
		log.Error(syncErr, "initial sync had errors (continuing)")
	}

	a.initialSyncDone = true

	// Mark startup/readiness probes as passing. When deployed as a native
	// sidecar (initContainer with restartPolicy: Always), this signals K8s
	// to proceed with starting the gateway container.
	a.HealthServer.MarkReady()
	log.Info("initial sync complete, startup probe now passing")

	// After the gateway finishes commissioning (first boot), re-sync files
	// and trigger a scan. Ignition's commissioning can overwrite resources
	// (e.g. security-properties) with defaults. This post-commission sync
	// restores the git-sourced config.
	go a.postCommissionSync(ctx, result.Commit, result.Ref)

	// Start watcher in background.
	go a.Watcher.Run(ctx)

	// Main loop: watch for trigger events.
	for {
		select {
		case <-ctx.Done():
			log.Info("shutting down")
			return nil
		case <-a.Watcher.Events():
			log.Info("sync triggered")
			a.handleSyncTrigger(ctx, gitURL, auth)
		}
	}
}

// postCommissionSync waits for the gateway to become responsive after first boot,
// then forces a re-sync and scan. This is needed because Ignition's commissioning
// process can overwrite config resources (e.g. security-properties) with defaults.
// Since the agent syncs as a native sidecar BEFORE the gateway starts, the
// commissioning defaults would otherwise shadow the git-sourced config.
func (a *Agent) postCommissionSync(ctx context.Context, commit, ref string) {
	log := logf.FromContext(ctx).WithName("post-commission")

	// Poll until gateway port is responding. We can't use HealthCheck() here
	// because it requires API token auth, and the commissioning defaults may
	// have overwritten the security-properties that grant the token access.
	// Instead, check for any HTTP response (even 401/403 means the gateway is up).
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}

		if err := a.IgnitionAPI.PortCheck(); err != nil {
			log.V(1).Info("gateway not ready yet", "error", err)
			continue
		}

		log.Info("gateway responsive, running post-commission re-sync")
		if err := a.syncOnce(ctx, commit, ref, false); err != nil {
			log.Error(err, "post-commission sync failed")
		} else {
			log.Info("post-commission sync complete")
		}
		return
	}
}

// waitForMetadata polls for the metadata ConfigMap until it's available.
func (a *Agent) waitForMetadata(ctx context.Context) (*Metadata, error) {
	log := logf.FromContext(ctx)

	for {
		meta, err := ReadMetadataConfigMap(ctx, a.K8sClient, a.Config.CRNamespace, a.Config.CRName)
		if err == nil && meta.Commit != "" {
			return meta, nil
		}

		if err != nil {
			log.V(1).Info("metadata not available yet, retrying", "error", err)
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

// handleSyncTrigger reads the latest metadata and performs a sync if needed.
func (a *Agent) handleSyncTrigger(ctx context.Context, gitURL string, auth transport.AuthMethod) {
	log := logf.FromContext(ctx).WithName("sync")

	meta, err := ReadMetadataConfigMap(ctx, a.K8sClient, a.Config.CRNamespace, a.Config.CRName)
	if err != nil {
		log.Error(err, "failed to read metadata ConfigMap")
		return
	}

	if meta.Paused == "true" {
		log.Info("CR is paused, skipping sync")
		return
	}

	// Check if commit changed.
	if meta.Commit == a.lastSyncedCommit {
		log.V(1).Info("commit unchanged, skipping sync", "commit", meta.Commit)
		return
	}

	log.Info("new commit detected", "old", a.lastSyncedCommit, "new", meta.Commit, "ref", meta.Ref)

	// Fetch and checkout new commit.
	result, err := a.GitClient.CloneOrFetch(ctx, gitURL, meta.Ref, a.Config.RepoPath, auth)
	if err != nil {
		log.Error(err, "git fetch failed")
		a.reportError(ctx, meta.Commit, meta.Ref, fmt.Sprintf("git fetch: %v", err))
		return
	}

	log.Info("git updated", "commit", result.Commit)

	// Pre-sync designer session check.
	profile, err := fetchSyncProfile(ctx, a.K8sClient, a.Config.CRNamespace, a.Config.SyncProfileName)
	if err != nil {
		log.Error(err, "failed to fetch SyncProfile for designer check")
		a.reportError(ctx, result.Commit, result.Ref, fmt.Sprintf("fetching profile: %v", err))
		return
	}

	if !profile.Paused && !profile.DryRun {
		if blocked := a.checkDesignerSessions(ctx, profile.DesignerSessionPolicy, result.Commit, result.Ref); blocked {
			return
		}
	}

	if syncErr := a.syncOnce(ctx, result.Commit, result.Ref, false); syncErr != nil {
		log.Error(syncErr, "sync had errors")
	}
}

// checkDesignerSessions enforces the designer session policy before sync.
// Returns true if the sync should be skipped (blocked or failed).
func (a *Agent) checkDesignerSessions(ctx context.Context, policy, commit, ref string) bool {
	log := logf.FromContext(ctx).WithName("designer-check")

	if policy == "" {
		policy = "proceed"
	}

	sessions, err := a.IgnitionAPI.GetDesignerSessions(ctx)
	if err != nil {
		log.Info("failed to query designer sessions (continuing sync)", "error", err)
		return false
	}

	if len(sessions) == 0 {
		return false
	}

	// Format session info for logging.
	sessionInfo := formatDesignerSessions(sessions)

	switch policy {
	case "proceed":
		log.Info("designer sessions active, proceeding per policy", "sessions", sessionInfo)
		return false

	case "fail":
		log.Info("designer sessions active, aborting sync per policy", "sessions", sessionInfo)
		a.event(corev1.EventTypeWarning, conditions.ReasonDesignerSessionsBlocked, "Designer sessions blocked sync: %s", sessionInfo)
		a.reportError(ctx, commit, ref, fmt.Sprintf("designer sessions active (policy=fail): %s", sessionInfo))
		return true

	case "wait":
		log.Info("designer sessions active, waiting for close", "sessions", sessionInfo)
		a.setDesignerBlocked(ctx, true)
		defer a.setDesignerBlocked(ctx, false)

		// Retry every 10s for up to 5 minutes.
		timeout := time.After(5 * time.Minute)
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return true
			case <-timeout:
				log.Info("timed out waiting for designer sessions to close", "sessions", sessionInfo)
				a.event(corev1.EventTypeWarning, conditions.ReasonDesignerSessionsBlocked, "Designer sessions blocked sync (5m timeout): %s", sessionInfo)
				a.reportError(ctx, commit, ref, fmt.Sprintf("designer sessions still active after 5m timeout: %s", sessionInfo))
				return true
			case <-ticker.C:
				sessions, err = a.IgnitionAPI.GetDesignerSessions(ctx)
				if err != nil {
					log.Info("designer check failed during wait (continuing sync)", "error", err)
					return false
				}
				if len(sessions) == 0 {
					log.Info("designer sessions closed, proceeding with sync")
					return false
				}
				log.V(1).Info("still waiting for designer sessions", "sessions", formatDesignerSessions(sessions))
			}
		}

	default:
		log.Info("unknown designer session policy, proceeding", "policy", policy)
		return false
	}
}

// setDesignerBlocked updates the DesignerSessionsBlocked field in the status ConfigMap.
func (a *Agent) setDesignerBlocked(ctx context.Context, blocked bool) {
	status := &stokertypes.GatewayStatus{
		SyncStatus:              stokertypes.SyncStatusPending,
		AgentVersion:            agentVersion,
		LastSyncTime:            time.Now().UTC().Format(time.RFC3339),
		DesignerSessionsBlocked: blocked,
	}
	if a.lastSyncedCommit != "" {
		status.SyncedCommit = a.lastSyncedCommit
	}
	_ = WriteStatusConfigMap(ctx, a.K8sClient, a.Config.CRNamespace, a.Config.CRName, a.Config.GatewayName, status)
}

// formatDesignerSessions builds a human-readable summary of active sessions.
func formatDesignerSessions(sessions []ignition.DesignerSession) string {
	parts := make([]string, len(sessions))
	for i, s := range sessions {
		parts[i] = fmt.Sprintf("%s on %s", s.User, s.Project)
	}
	return strings.Join(parts, ", ")
}

// syncOnce performs a single sync cycle: copy files, trigger scan, report status.
func (a *Agent) syncOnce(ctx context.Context, commit, ref string, isInitial bool) error {
	log := logf.FromContext(ctx).WithName("sync")

	syncResult, profileName, isDryRun, err := a.syncWithProfile(ctx)

	if err != nil {
		a.reportError(ctx, commit, ref, fmt.Sprintf("sync engine: %v", err))
		a.event(corev1.EventTypeWarning, conditions.ReasonSyncFailed, "Sync failed: %v", err)
		return fmt.Errorf("sync engine: %w", err)
	}

	log.Info("files synced",
		"added", syncResult.FilesAdded,
		"modified", syncResult.FilesModified,
		"deleted", syncResult.FilesDeleted,
		"projects", syncResult.ProjectsSynced,
		"duration", syncResult.Duration,
		"profile", profileName,
		"dryRun", isDryRun,
	)

	// Trigger Ignition scan API (skip on initial sync and dry-run).
	var scanResultStr string
	if isDryRun {
		log.Info("dry-run mode, skipping scan API")
	} else if !isInitial {
		filesChanged := int32(syncResult.FilesAdded + syncResult.FilesModified + syncResult.FilesDeleted)
		if filesChanged > 0 {
			log.Info("triggering Ignition scan API")
			scanResult := a.IgnitionAPI.TriggerScan()
			scanResultStr = scanResult.String()
			if scanResult.Error != "" {
				log.Info("scan API warning (non-fatal)", "error", scanResult.Error)
			} else {
				log.Info("scan complete", "result", scanResultStr)
			}
		}
	} else {
		// On initial sync, attempt a health check but don't require it.
		if err := a.IgnitionAPI.HealthCheck(); err != nil {
			log.Info("gateway health check failed (expected on initial sync)", "error", err)
			scanResultStr = fmt.Sprintf("health check failed: %v", err)
		}
	}

	// Determine status.
	syncStatus := stokertypes.SyncStatusSynced
	var errorMsg string
	if scanResultStr != "" && strings.Contains(scanResultStr, "error") {
		syncStatus = stokertypes.SyncStatusError
		errorMsg = scanResultStr
	}

	// Report status to ConfigMap.
	filesChanged := int32(syncResult.FilesAdded + syncResult.FilesModified + syncResult.FilesDeleted)
	status := &stokertypes.GatewayStatus{
		SyncStatus:       syncStatus,
		SyncedCommit:     commit,
		SyncedRef:        ref,
		LastSyncTime:     time.Now().UTC().Format(time.RFC3339),
		LastSyncDuration: syncResult.Duration.Round(time.Millisecond).String(),
		AgentVersion:     agentVersion,
		LastScanResult:   scanResultStr,
		FilesChanged:     filesChanged,
		ProjectsSynced:   syncResult.ProjectsSynced,
		ErrorMessage:     errorMsg,
		SyncProfileName:  profileName,
		DryRun:           isDryRun,
	}

	if isDryRun && syncResult.DryRunDiff != nil {
		status.DryRunDiffAdded = int32(len(syncResult.DryRunDiff.Added))
		status.DryRunDiffModified = int32(len(syncResult.DryRunDiff.Modified))
		status.DryRunDiffDeleted = int32(len(syncResult.DryRunDiff.Deleted))
	}

	if err := WriteStatusConfigMap(ctx, a.K8sClient, a.Config.CRNamespace, a.Config.CRName, a.Config.GatewayName, status); err != nil {
		log.Error(err, "failed to write status ConfigMap")
	} else {
		log.Info("status written to ConfigMap", "gateway", a.Config.GatewayName, "status", syncStatus)
	}

	a.event(corev1.EventTypeNormal, conditions.ReasonSyncCompleted,
		"Sync completed on %s: commit %s, %d file(s) changed", a.Config.GatewayName, commit[:min(12, len(commit))], filesChanged)

	a.lastSyncedCommit = commit
	return nil
}

// syncWithProfile fetches the SyncProfile, builds a plan, and executes it.
// Returns the sync result, profile name, dry-run flag, and any error.
func (a *Agent) syncWithProfile(ctx context.Context) (*syncengine.SyncResult, string, bool, error) {
	log := logf.FromContext(ctx).WithName("profile-sync")
	profileName := a.Config.SyncProfileName

	// Fetch SyncProfile CR.
	log.Info("fetching SyncProfile", "name", profileName)
	profile, err := fetchSyncProfile(ctx, a.K8sClient, a.Config.CRNamespace, profileName)
	if err != nil {
		return nil, profileName, false, fmt.Errorf("fetching profile: %w", err)
	}

	// Check if profile is paused.
	if profile.Paused {
		log.Info("SyncProfile is paused, returning zero-change result")
		return &syncengine.SyncResult{}, profileName, profile.DryRun, nil
	}

	// Read metadata for CR-level excludes.
	meta, err := ReadMetadataConfigMap(ctx, a.K8sClient, a.Config.CRNamespace, a.Config.CRName)
	if err != nil {
		return nil, profileName, profile.DryRun, fmt.Errorf("reading metadata for excludes: %w", err)
	}

	// Read pod labels for template context.
	var pod corev1.Pod
	if err := a.K8sClient.Get(ctx, client.ObjectKey{Name: a.Config.PodName, Namespace: a.Config.PodNamespace}, &pod); err != nil {
		return nil, profileName, profile.DryRun, fmt.Errorf("reading pod labels: %w", err)
	}

	// Build template context.
	tmplCtx := buildTemplateContext(a.Config, meta, profile.Vars, pod.Labels)

	// Build sync plan.
	crExcludes := parseCRExcludes(meta.ExcludePatterns)
	plan, err := buildSyncPlan(profile, tmplCtx, a.Config.RepoPath, a.Config.DataPath, crExcludes)
	if err != nil {
		return nil, profileName, profile.DryRun, fmt.Errorf("building sync plan: %w", err)
	}

	// Add engine-level excludes to the plan.
	plan.ExcludePatterns = append(plan.ExcludePatterns, a.SyncEngine.ExcludePatterns...)

	log.Info("executing sync plan",
		"mappings", len(plan.Mappings),
		"dryRun", plan.DryRun,
		"excludes", len(plan.ExcludePatterns),
	)

	// Execute the plan.
	result, err := a.SyncEngine.ExecutePlan(plan)
	if err != nil {
		return nil, profileName, profile.DryRun, fmt.Errorf("executing plan: %w", err)
	}

	return result, profileName, profile.DryRun, nil
}

// reportError writes an error status to the status ConfigMap.
func (a *Agent) reportError(ctx context.Context, commit, ref, errMsg string) {
	a.event(corev1.EventTypeWarning, conditions.ReasonSyncFailed, "%s", errMsg)
	status := &stokertypes.GatewayStatus{
		SyncStatus:   stokertypes.SyncStatusError,
		SyncedCommit: commit,
		SyncedRef:    ref,
		LastSyncTime: time.Now().UTC().Format(time.RFC3339),
		AgentVersion: agentVersion,
		ErrorMessage: errMsg,
	}
	_ = WriteStatusConfigMap(ctx, a.K8sClient, a.Config.CRNamespace, a.Config.CRName, a.Config.GatewayName, status)
}

// event emits a K8s event on the cached Stoker CR. No-op if recorder or
// stkRef is nil (e.g. during tests or when recorder setup failed).
func (a *Agent) event(eventType, reason, msgFmt string, args ...any) {
	if a.Recorder == nil || a.stkRef == nil {
		return
	}
	a.Recorder.Eventf(a.stkRef, eventType, reason, msgFmt, args...)
}

// fetchISyncRef fetches the Stoker CR once and caches it as event target.
func (a *Agent) fetchISyncRef(ctx context.Context) {
	var stk stokerv1alpha1.Stoker
	key := client.ObjectKey{Namespace: a.Config.CRNamespace, Name: a.Config.CRName}
	if err := a.K8sClient.Get(ctx, key, &stk); err != nil {
		logf.FromContext(ctx).Info("could not fetch Stoker for events (non-fatal)", "error", err)
		return
	}
	a.stkRef = &stk
}

// resolveFileAuth builds a go-git transport.AuthMethod from mounted credential files.
func (a *Agent) resolveFileAuth() transport.AuthMethod {
	// SSH key takes priority.
	if sshKey := a.Config.GitSSHKey(); len(sshKey) > 0 {
		publicKey, err := gogitssh.NewPublicKeys("git", sshKey, "")
		if err == nil {
			publicKey.HostKeyCallback = ssh.InsecureIgnoreHostKey()
			return publicKey
		}
	}

	// Token auth.
	if token := a.Config.GitToken(); token != "" {
		return &gogithttp.BasicAuth{
			Username: "x-access-token",
			Password: token,
		}
	}

	return nil
}
