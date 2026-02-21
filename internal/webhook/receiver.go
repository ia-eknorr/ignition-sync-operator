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

package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	syncv1alpha1 "github.com/inductiveautomation/ignition-sync-operator/api/v1alpha1"
	synctypes "github.com/inductiveautomation/ignition-sync-operator/pkg/types"
)

const (
	maxPayloadBytes = 1 << 20 // 1 MiB

	// SourceGeneric is the source identifier for generic webhook payloads.
	SourceGeneric = "generic"
)

// Receiver is an HTTP server that receives webhook payloads and annotates
// IgnitionSync CRs with the requested ref. It implements manager.Runnable.
type Receiver struct {
	Client     client.Client
	HMACSecret string
	Port       int32
}

// Start starts the webhook HTTP server. Blocks until ctx is cancelled.
func (rv *Receiver) Start(ctx context.Context) error {
	log := logf.FromContext(ctx).WithName("webhook-receiver")

	mux := http.NewServeMux()
	mux.HandleFunc("POST /webhook/{namespace}/{crName}", rv.handleWebhook)

	addr := fmt.Sprintf(":%d", rv.Port)
	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(_ net.Listener) context.Context { return ctx },
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	log.Info("starting webhook receiver", "addr", addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("webhook server error: %w", err)
	}
	return nil
}

func (rv *Receiver) handleWebhook(w http.ResponseWriter, r *http.Request) {
	log := logf.FromContext(r.Context()).WithName("webhook-receiver")

	namespace := r.PathValue("namespace")
	crName := r.PathValue("crName")

	// Read body (capped at 1 MiB)
	body, err := io.ReadAll(io.LimitReader(r.Body, maxPayloadBytes))
	if err != nil {
		log.Error(err, "failed to read request body")
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	// Validate HMAC BEFORE any CR lookup — prevents enumeration attacks
	if rv.HMACSecret != "" {
		signature := r.Header.Get("X-Hub-Signature-256")
		if err := ValidateHMAC(body, signature, rv.HMACSecret); err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}

	// Parse ref from payload (auto-detect format)
	ref, source := parsePayload(body)
	if ref == "" {
		http.Error(w, `{"error":"no ref found in payload"}`, http.StatusBadRequest)
		return
	}

	// Look up the IgnitionSync CR
	var isync syncv1alpha1.IgnitionSync
	key := types.NamespacedName{Name: crName, Namespace: namespace}
	if err := rv.Client.Get(r.Context(), key, &isync); err != nil {
		log.Error(err, "CR not found", "namespace", namespace, "name", crName)
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	// Check if the ref is already set (idempotent)
	if isync.Annotations != nil && isync.Annotations[synctypes.AnnotationRequestedRef] == ref {
		writeJSON(w, http.StatusOK, map[string]any{
			"accepted": true,
			"ref":      ref,
			"message":  "ref already set",
		})
		return
	}

	// Annotate the CR
	if isync.Annotations == nil {
		isync.Annotations = make(map[string]string)
	}
	isync.Annotations[synctypes.AnnotationRequestedRef] = ref
	isync.Annotations[synctypes.AnnotationRequestedAt] = time.Now().UTC().Format(time.RFC3339)
	isync.Annotations[synctypes.AnnotationRequestedBy] = source

	if err := rv.Client.Update(r.Context(), &isync); err != nil {
		log.Error(err, "failed to annotate CR", "namespace", namespace, "name", crName)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	log.Info("webhook accepted", "namespace", namespace, "cr", crName, "ref", ref, "source", source)
	writeJSON(w, http.StatusAccepted, map[string]any{
		"accepted": true,
		"ref":      ref,
	})
}

// parsePayload auto-detects the payload format and extracts the ref.
// Returns (ref, source) where source identifies the detected format.
func parsePayload(body []byte) (string, string) {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return "", ""
	}

	// 1. GitHub release: { "action": "published", "release": { "tag_name": "2.0.0" } }
	if release, ok := raw["release"].(map[string]any); ok {
		if tag, ok := release["tag_name"].(string); ok && tag != "" {
			return tag, "github"
		}
	}

	// 2. ArgoCD notification: { "app": { "metadata": { "annotations": { "git.ref": "2.0.0" } } } }
	if app, ok := raw["app"].(map[string]any); ok {
		if meta, ok := app["metadata"].(map[string]any); ok {
			if anns, ok := meta["annotations"].(map[string]any); ok {
				if ref, ok := anns["git.ref"].(string); ok && ref != "" {
					return ref, "argocd"
				}
			}
		}
	}

	// 3. Kargo promotion: { "freight": { "commits": [{ "tag": "2.0.0" }] } }
	if freight, ok := raw["freight"].(map[string]any); ok {
		if commits, ok := freight["commits"].([]any); ok && len(commits) > 0 {
			if commit, ok := commits[0].(map[string]any); ok {
				if tag, ok := commit["tag"].(string); ok && tag != "" {
					return tag, "kargo"
				}
			}
		}
	}

	// 4. Generic: { "ref": "2.0.0" }
	if ref, ok := raw["ref"].(string); ok && ref != "" {
		return ref, SourceGeneric
	}

	return "", ""
}

func writeJSON(w http.ResponseWriter, status int, data map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
