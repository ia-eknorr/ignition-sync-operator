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
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/inductiveautomation/ignition-sync-operator/internal/git"
	"github.com/inductiveautomation/ignition-sync-operator/internal/ignition"
	"github.com/inductiveautomation/ignition-sync-operator/internal/syncengine"
	synctypes "github.com/inductiveautomation/ignition-sync-operator/pkg/types"
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

	lastSyncedCommit string
	initialSyncDone  bool
}

// New creates a new Agent with all dependencies wired.
func New(cfg *Config, k8sClient client.Client) *Agent {
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
	}
}

// Run starts the agent main loop. Blocks until ctx is cancelled.
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

	// Start health server in background.
	go a.HealthServer.Start(ctx)

	// Read metadata ConfigMap to get git URL and commit.
	log.Info("reading metadata ConfigMap")
	meta, err := a.waitForMetadata(ctx)
	if err != nil {
		return fmt.Errorf("waiting for metadata: %w", err)
	}

	log.Info("metadata loaded", "gitURL", meta.GitURL, "commit", meta.Commit, "ref", meta.Ref)

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
		return fmt.Errorf("initial clone: %w", err)
	}
	log.Info("clone complete", "commit", result.Commit)

	// Initial sync (blocking).
	log.Info("performing initial sync")
	syncErr := a.syncOnce(ctx, result.Commit, result.Ref, true)
	if syncErr != nil {
		log.Error(syncErr, "initial sync had errors (continuing)")
	}

	a.initialSyncDone = true
	a.HealthServer.MarkReady()
	log.Info("initial sync complete, agent ready")

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
	if syncErr := a.syncOnce(ctx, result.Commit, result.Ref, false); syncErr != nil {
		log.Error(syncErr, "sync had errors")
	}
}

// syncOnce performs a single sync cycle: copy files, trigger scan, report status.
func (a *Agent) syncOnce(ctx context.Context, commit, ref string, isInitial bool) error {
	log := logf.FromContext(ctx).WithName("sync")

	srcRoot := a.Config.SourceRoot()
	dstRoot := a.Config.DataPath

	// Run sync engine.
	syncResult, err := a.SyncEngine.Sync(srcRoot, dstRoot)
	if err != nil {
		a.reportError(ctx, commit, ref, fmt.Sprintf("sync engine: %v", err))
		return fmt.Errorf("sync engine: %w", err)
	}

	log.Info("files synced",
		"added", syncResult.FilesAdded,
		"modified", syncResult.FilesModified,
		"deleted", syncResult.FilesDeleted,
		"projects", syncResult.ProjectsSynced,
		"duration", syncResult.Duration,
	)

	// Trigger Ignition scan API (skip on initial sync — Ignition auto-scans on first boot).
	var scanResultStr string
	if !isInitial {
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
	syncStatus := synctypes.SyncStatusSynced
	var errorMsg string
	if scanResultStr != "" && strings.Contains(scanResultStr, "error") {
		syncStatus = synctypes.SyncStatusError
		errorMsg = scanResultStr
	}

	// Report status to ConfigMap.
	filesChanged := int32(syncResult.FilesAdded + syncResult.FilesModified + syncResult.FilesDeleted)
	status := &synctypes.GatewayStatus{
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
	}

	if err := WriteStatusConfigMap(ctx, a.K8sClient, a.Config.CRNamespace, a.Config.CRName, a.Config.GatewayName, status); err != nil {
		log.Error(err, "failed to write status ConfigMap")
	} else {
		log.Info("status written to ConfigMap", "gateway", a.Config.GatewayName, "status", syncStatus)
	}

	a.lastSyncedCommit = commit
	return nil
}

// reportError writes an error status to the status ConfigMap.
func (a *Agent) reportError(ctx context.Context, commit, ref, errMsg string) {
	status := &synctypes.GatewayStatus{
		SyncStatus:   synctypes.SyncStatusError,
		SyncedCommit: commit,
		SyncedRef:    ref,
		LastSyncTime: time.Now().UTC().Format(time.RFC3339),
		AgentVersion: agentVersion,
		ErrorMessage: errMsg,
	}
	_ = WriteStatusConfigMap(ctx, a.K8sClient, a.Config.CRNamespace, a.Config.CRName, a.Config.GatewayName, status)
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
