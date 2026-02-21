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
	"encoding/json"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	syncv1alpha1 "github.com/inductiveautomation/ignition-sync-operator/api/v1alpha1"
	"github.com/inductiveautomation/ignition-sync-operator/pkg/conditions"
	synctypes "github.com/inductiveautomation/ignition-sync-operator/pkg/types"
)

// findIgnitionSyncForPod reads the ignition-sync.io/cr-name annotation from a pod
// and returns a reconcile.Request for the matching IgnitionSync CR in the same namespace.
// Returns nil if the annotation is not present.
func (r *IgnitionSyncReconciler) findIgnitionSyncForPod(ctx context.Context, pod client.Object) []reconcile.Request {
	crName, ok := pod.GetAnnotations()[synctypes.AnnotationCRName]
	if !ok || crName == "" {
		return nil
	}

	return []reconcile.Request{
		{
			NamespacedName: types.NamespacedName{
				Name:      crName,
				Namespace: pod.GetNamespace(),
			},
		},
	}
}

// discoverGateways lists all pods in the CR's namespace with annotation ignition-sync.io/cr-name
// matching isync.Name. For each matching pod in Running phase, it builds a DiscoveredGateway.
func (r *IgnitionSyncReconciler) discoverGateways(ctx context.Context, isync *syncv1alpha1.IgnitionSync) ([]syncv1alpha1.DiscoveredGateway, error) {
	log := logf.FromContext(ctx)

	// List all pods in the namespace
	var podList corev1.PodList
	if err := r.List(ctx, &podList, client.InNamespace(isync.Namespace)); err != nil {
		return nil, fmt.Errorf("listing pods: %w", err)
	}

	discovered := make([]syncv1alpha1.DiscoveredGateway, 0, len(podList.Items))

	for _, pod := range podList.Items {
		// Filter by annotation
		crName, ok := pod.Annotations[synctypes.AnnotationCRName]
		if !ok || crName != isync.Name {
			continue
		}

		// Only include Running pods
		if pod.Status.Phase != corev1.PodRunning {
			continue
		}

		// Determine gateway name
		gatewayName := pod.Name
		if nameFromAnnotation, ok := pod.Annotations[synctypes.AnnotationGatewayName]; ok && nameFromAnnotation != "" {
			gatewayName = nameFromAnnotation
		} else if nameFromLabel, ok := pod.Labels["app.kubernetes.io/name"]; ok && nameFromLabel != "" {
			gatewayName = nameFromLabel
		}

		// Get sync profile from annotation
		syncProfile := pod.Annotations[synctypes.AnnotationSyncProfile]

		// Detect missing sidecar: pod has inject annotation but no sync-agent container
		syncStatus := synctypes.SyncStatusPending
		if pod.Annotations[synctypes.AnnotationInject] == "true" && !hasSyncAgent(&pod) {
			syncStatus = synctypes.SyncStatusMissingSidecar
			r.Recorder.Eventf(isync, corev1.EventTypeWarning, "MissingSidecar",
				"Pod %s has inject annotation but no sync-agent sidecar — webhook may have been unavailable during pod creation. Delete and recreate the pod.", pod.Name)
		}

		gateway := syncv1alpha1.DiscoveredGateway{
			Name:        gatewayName,
			Namespace:   pod.Namespace,
			PodName:     pod.Name,
			SyncProfile: syncProfile,
			SyncStatus:  syncStatus,
		}

		discovered = append(discovered, gateway)
	}

	log.Info("discovered gateways", "count", len(discovered))
	return discovered, nil
}

// hasSyncAgent checks if a pod has the sync-agent sidecar container.
func hasSyncAgent(pod *corev1.Pod) bool {
	for _, c := range pod.Spec.InitContainers {
		if c.Name == "sync-agent" {
			return true
		}
	}
	return false
}

// collectGatewayStatus reads the ConfigMap ignition-sync-status-{isync.Name} in isync.Namespace
// and enriches each gateway with its sync status data. If the ConfigMap doesn't exist or a
// gateway's status key is missing, the gateway remains with SyncStatus="Pending".
func (r *IgnitionSyncReconciler) collectGatewayStatus(ctx context.Context, isync *syncv1alpha1.IgnitionSync, gateways []syncv1alpha1.DiscoveredGateway) []syncv1alpha1.DiscoveredGateway {
	log := logf.FromContext(ctx)

	cmName := fmt.Sprintf("ignition-sync-status-%s", isync.Name)
	cm := &corev1.ConfigMap{}
	key := types.NamespacedName{Name: cmName, Namespace: isync.Namespace}

	if err := r.Get(ctx, key, cm); err != nil {
		if errors.IsNotFound(err) {
			log.Info("status ConfigMap not found, gateways remain Pending", "configmap", cmName)
		} else {
			log.Error(err, "failed to get status ConfigMap", "configmap", cmName)
		}
		return gateways
	}

	// Enrich each gateway with its status
	for i := range gateways {
		statusJSON, ok := cm.Data[gateways[i].Name]
		if !ok || statusJSON == "" {
			continue
		}

		var status synctypes.GatewayStatus
		if err := json.Unmarshal([]byte(statusJSON), &status); err != nil {
			log.Error(err, "failed to unmarshal gateway status", "gateway", gateways[i].Name)
			continue
		}

		// Map status fields onto DiscoveredGateway
		gateways[i].SyncStatus = status.SyncStatus
		gateways[i].SyncedCommit = status.SyncedCommit
		gateways[i].SyncedRef = status.SyncedRef
		gateways[i].LastSyncDuration = status.LastSyncDuration
		gateways[i].AgentVersion = status.AgentVersion
		gateways[i].LastScanResult = status.LastScanResult
		gateways[i].FilesChanged = status.FilesChanged
		gateways[i].ProjectsSynced = status.ProjectsSynced

		// Parse lastSyncTime as RFC3339
		if status.LastSyncTime != "" {
			t, err := time.Parse(time.RFC3339, status.LastSyncTime)
			if err != nil {
				log.Error(err, "failed to parse lastSyncTime", "gateway", gateways[i].Name, "time", status.LastSyncTime)
			} else {
				mt := metav1.NewTime(t)
				gateways[i].LastSyncTime = &mt
			}
		}
	}

	return gateways
}

// updateAllGatewaysSyncedCondition counts how many gateways are synced and sets
// the AllGatewaysSynced condition accordingly.
func (r *IgnitionSyncReconciler) updateAllGatewaysSyncedCondition(ctx context.Context, isync *syncv1alpha1.IgnitionSync) {
	totalGateways := len(isync.Status.DiscoveredGateways)

	if totalGateways == 0 {
		r.setCondition(ctx, isync, conditions.TypeAllGatewaysSynced, metav1.ConditionFalse,
			conditions.ReasonNoGateways, "No gateways discovered")
		return
	}

	syncedCount := 0
	missingSidecarCount := 0
	for _, gw := range isync.Status.DiscoveredGateways {
		if gw.SyncStatus == synctypes.SyncStatusSynced {
			syncedCount++
		}
		if gw.SyncStatus == synctypes.SyncStatusMissingSidecar {
			missingSidecarCount++
		}
	}

	if syncedCount == totalGateways {
		message := fmt.Sprintf("%d/%d gateways synced", syncedCount, totalGateways)
		r.setCondition(ctx, isync, conditions.TypeAllGatewaysSynced, metav1.ConditionTrue,
			conditions.ReasonSyncSucceeded, message)
	} else {
		message := fmt.Sprintf("%d/%d gateways synced", syncedCount, totalGateways)
		if missingSidecarCount > 0 {
			message = fmt.Sprintf("%d/%d gateways synced (%d missing sidecar)", syncedCount, totalGateways, missingSidecarCount)
		}
		r.setCondition(ctx, isync, conditions.TypeAllGatewaysSynced, metav1.ConditionFalse,
			conditions.ReasonSyncInProgress, message)
	}

	// Update SidecarInjected condition
	if missingSidecarCount > 0 {
		r.setCondition(ctx, isync, conditions.TypeSidecarInjected, metav1.ConditionFalse,
			conditions.ReasonSidecarMissing, fmt.Sprintf("%d gateway(s) missing sync-agent sidecar", missingSidecarCount))
	} else {
		r.setCondition(ctx, isync, conditions.TypeSidecarInjected, metav1.ConditionTrue,
			conditions.ReasonSidecarPresent, "All gateways have sync-agent sidecar")
	}
}

// updateReadyCondition sets the Ready condition based on RefResolved and AllGatewaysSynced.
// Ready=True only when both RefResolved=True AND AllGatewaysSynced=True.
func (r *IgnitionSyncReconciler) updateReadyCondition(ctx context.Context, isync *syncv1alpha1.IgnitionSync) {
	refResolved := false
	allGatewaysSynced := false

	for _, cond := range isync.Status.Conditions {
		if cond.Type == conditions.TypeRefResolved && cond.Status == metav1.ConditionTrue {
			refResolved = true
		}
		if cond.Type == conditions.TypeAllGatewaysSynced && cond.Status == metav1.ConditionTrue {
			allGatewaysSynced = true
		}
	}

	if refResolved && allGatewaysSynced {
		r.setCondition(ctx, isync, conditions.TypeReady, metav1.ConditionTrue,
			conditions.ReasonSyncSucceeded, "All gateways synced")
	} else if !refResolved {
		r.setCondition(ctx, isync, conditions.TypeReady, metav1.ConditionFalse,
			conditions.ReasonReconciling, "Ref not resolved")
	} else {
		r.setCondition(ctx, isync, conditions.TypeReady, metav1.ConditionFalse,
			conditions.ReasonReconciling, "Waiting for gateways to sync")
	}
}

// updateProfileGatewayCounts counts how many discovered gateways reference each
// SyncProfile and patches the gatewayCount on each profile's status.
func (r *IgnitionSyncReconciler) updateProfileGatewayCounts(ctx context.Context, isync *syncv1alpha1.IgnitionSync) {
	log := logf.FromContext(ctx)

	// Count gateways per profile
	counts := make(map[string]int32)
	for _, gw := range isync.Status.DiscoveredGateways {
		if gw.SyncProfile != "" {
			counts[gw.SyncProfile]++
		}
	}

	// List all profiles in the namespace to update counts (including zeroing out stale ones)
	var profileList syncv1alpha1.SyncProfileList
	if err := r.List(ctx, &profileList, client.InNamespace(isync.Namespace)); err != nil {
		log.Error(err, "failed to list SyncProfiles for gateway count update")
		return
	}

	for i := range profileList.Items {
		profile := &profileList.Items[i]
		newCount := counts[profile.Name]
		if profile.Status.GatewayCount == newCount {
			continue
		}
		base := profile.DeepCopy()
		profile.Status.GatewayCount = newCount
		if err := r.Status().Patch(ctx, profile, client.MergeFrom(base)); err != nil {
			log.Error(err, "failed to update SyncProfile gatewayCount", "profile", profile.Name)
		}
	}
}
