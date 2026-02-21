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
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	syncv1alpha1 "github.com/inductiveautomation/ignition-sync-operator/api/v1alpha1"
	synctypes "github.com/inductiveautomation/ignition-sync-operator/pkg/types"
)

const testRef = "v2.0.0"

// --- Payload parsing tests ---

func TestParsePayload_Generic(t *testing.T) {
	body := []byte(`{"ref":"v2.0.0"}`)
	ref, source := parsePayload(body)
	if ref != testRef {
		t.Fatalf("expected ref=v2.0.0, got %q", ref)
	}
	if source != SourceGeneric {
		t.Fatalf("expected source=generic, got %q", source)
	}
}

func TestParsePayload_GitHub(t *testing.T) {
	body := []byte(`{"action":"published","release":{"tag_name":"v3.0.0"}}`)
	ref, source := parsePayload(body)
	if ref != "v3.0.0" {
		t.Fatalf("expected ref=v3.0.0, got %q", ref)
	}
	if source != "github" {
		t.Fatalf("expected source=github, got %q", source)
	}
}

func TestParsePayload_ArgoCD(t *testing.T) {
	body := []byte(`{"app":{"metadata":{"annotations":{"git.ref":"v4.0.0"}}}}`)
	ref, source := parsePayload(body)
	if ref != "v4.0.0" {
		t.Fatalf("expected ref=v4.0.0, got %q", ref)
	}
	if source != "argocd" {
		t.Fatalf("expected source=argocd, got %q", source)
	}
}

func TestParsePayload_Kargo(t *testing.T) {
	body := []byte(`{"freight":{"commits":[{"tag":"v5.0.0"}]}}`)
	ref, source := parsePayload(body)
	if ref != "v5.0.0" {
		t.Fatalf("expected ref=v5.0.0, got %q", ref)
	}
	if source != "kargo" {
		t.Fatalf("expected source=kargo, got %q", source)
	}
}

func TestParsePayload_Empty(t *testing.T) {
	ref, _ := parsePayload([]byte(`{}`))
	if ref != "" {
		t.Fatalf("expected empty ref, got %q", ref)
	}
}

func TestParsePayload_InvalidJSON(t *testing.T) {
	ref, _ := parsePayload([]byte(`not json`))
	if ref != "" {
		t.Fatalf("expected empty ref, got %q", ref)
	}
}

// --- HTTP handler tests ---

func newTestReceiver(hmacSecret string, objects ...runtime.Object) (*Receiver, *http.ServeMux) {
	scheme := runtime.NewScheme()
	_ = syncv1alpha1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(objects...).
		Build()

	rv := &Receiver{
		Client:     fakeClient,
		HMACSecret: hmacSecret,
		Port:       9443,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /webhook/{namespace}/{crName}", rv.handleWebhook)
	return rv, mux
}

func testCR() *syncv1alpha1.IgnitionSync {
	return &syncv1alpha1.IgnitionSync{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-sync",
			Namespace: "default",
		},
		Spec: syncv1alpha1.IgnitionSyncSpec{
			Git: syncv1alpha1.GitSpec{
				Repo: "git@github.com:example/test.git",
				Ref:  "main",
			},
			Gateway: syncv1alpha1.GatewaySpec{
				APIKeySecretRef: syncv1alpha1.SecretKeyRef{
					Name: "api-key",
					Key:  "apiKey",
				},
			},
		},
	}
}

func TestHandler_AcceptsValidRequest(t *testing.T) {
	_, mux := newTestReceiver("", testCR())

	body := []byte(`{"ref":"v2.0.0"}`)
	req := httptest.NewRequest("POST", "/webhook/default/my-sync", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["ref"] != testRef {
		t.Fatalf("expected ref=v2.0.0 in response, got %v", resp["ref"])
	}
}

func TestHandler_ReturnsNotFoundForMissingCR(t *testing.T) {
	_, mux := newTestReceiver("") // no CRs

	body := []byte(`{"ref":"v2.0.0"}`)
	req := httptest.NewRequest("POST", "/webhook/default/nonexistent", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHandler_RejectsInvalidHMAC(t *testing.T) {
	_, mux := newTestReceiver("my-secret", testCR())

	body := []byte(`{"ref":"v2.0.0"}`)
	req := httptest.NewRequest("POST", "/webhook/default/my-sync", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", "sha256=invalid")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestHandler_AcceptsValidHMAC(t *testing.T) {
	secret := "test-secret"
	_, mux := newTestReceiver(secret, testCR())

	body := []byte(`{"ref":"v2.0.0"}`)
	sig := computeHMAC(body, secret)
	req := httptest.NewRequest("POST", "/webhook/default/my-sync", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", sig)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_AnnotatesCR(t *testing.T) {
	rv, mux := newTestReceiver("", testCR())

	body := []byte(`{"ref":"v2.0.0"}`)
	req := httptest.NewRequest("POST", "/webhook/default/my-sync", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}

	// Verify the CR was annotated
	var cr syncv1alpha1.IgnitionSync
	err := rv.Client.Get(req.Context(), types.NamespacedName{Name: "my-sync", Namespace: "default"}, &cr)
	if err != nil {
		t.Fatalf("failed to get CR: %v", err)
	}
	if cr.Annotations[synctypes.AnnotationRequestedRef] != testRef {
		t.Fatalf("expected requested-ref=v2.0.0, got %q", cr.Annotations[synctypes.AnnotationRequestedRef])
	}
	if cr.Annotations[synctypes.AnnotationRequestedBy] != SourceGeneric {
		t.Fatalf("expected requested-by=generic, got %q", cr.Annotations[synctypes.AnnotationRequestedBy])
	}
	if cr.Annotations[synctypes.AnnotationRequestedAt] == "" {
		t.Fatal("expected requested-at to be set")
	}
}

func TestHandler_DuplicateRefReturns200(t *testing.T) {
	cr := testCR()
	cr.Annotations = map[string]string{
		synctypes.AnnotationRequestedRef: testRef,
	}
	_, mux := newTestReceiver("", cr)

	body := []byte(`{"ref":"v2.0.0"}`)
	req := httptest.NewRequest("POST", "/webhook/default/my-sync", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	// Duplicate ref should return 200 (not 202)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for duplicate ref, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_BadPayload(t *testing.T) {
	_, mux := newTestReceiver("", testCR())

	body := []byte(`{"nothing":"here"}`)
	req := httptest.NewRequest("POST", "/webhook/default/my-sync", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandler_HMACValidatedBeforeCRLookup(t *testing.T) {
	// No CRs exist, but HMAC is set — should get 401, not 404
	_, mux := newTestReceiver("my-secret")

	body := []byte(`{"ref":"v2.0.0"}`)
	req := httptest.NewRequest("POST", "/webhook/default/nonexistent", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", "sha256=invalid")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 (HMAC before CR lookup), got %d", w.Code)
	}
}
