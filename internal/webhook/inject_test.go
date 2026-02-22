package webhook

import (
	"context"
	"encoding/json"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	stokerv1alpha1 "github.com/ia-eknorr/stoker-operator/api/v1alpha1"
	stokertypes "github.com/ia-eknorr/stoker-operator/pkg/types"
)

const testNamespace = "test-ns"

func newScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = stokerv1alpha1.AddToScheme(s)
	_ = corev1.AddToScheme(s)
	return s
}

func newInjector(objects ...runtime.Object) *PodInjector {
	s := newScheme()
	return &PodInjector{
		Client: fake.NewClientBuilder().
			WithScheme(s).
			WithRuntimeObjects(objects...).
			Build(),
		Decoder: admission.NewDecoder(s),
	}
}

func testStoker() *stokerv1alpha1.Stoker {
	return &stokerv1alpha1.Stoker{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-sync",
			Namespace: testNamespace,
		},
		Spec: stokerv1alpha1.StokerSpec{
			Git: stokerv1alpha1.GitSpec{
				Repo: "git@github.com:example/test.git",
				Ref:  "main",
				Auth: &stokerv1alpha1.GitAuthSpec{
					Token: &stokerv1alpha1.TokenAuth{
						SecretRef: stokerv1alpha1.SecretKeyRef{
							Name: "git-token-secret",
							Key:  "token",
						},
					},
				},
			},
			Gateway: stokerv1alpha1.GatewaySpec{
				Port: 8043,
				APIKeySecretRef: stokerv1alpha1.SecretKeyRef{
					Name: "api-key-secret",
					Key:  "apiKey",
				},
			},
		},
	}
}

func testSyncProfile() *stokerv1alpha1.SyncProfile {
	return &stokerv1alpha1.SyncProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-profile",
			Namespace: testNamespace,
		},
		Spec: stokerv1alpha1.SyncProfileSpec{
			Mappings: []stokerv1alpha1.SyncMapping{
				{Source: "config/", Destination: "config/"},
			},
		},
	}
}

func makeAdmissionRequest(pod *corev1.Pod) admission.Request {
	raw, _ := json.Marshal(pod)
	return admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Namespace: testNamespace,
			Object:    runtime.RawExtension{Raw: raw},
		},
	}
}

func basePod(annotations map[string]string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "gateway-0",
			Namespace:   testNamespace,
			Annotations: annotations,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "ignition-gateway", Image: "inductiveautomation/ignition:8.3.3"},
			},
		},
	}
}

// --- Test Cases ---

func TestInject_WithAllAnnotations(t *testing.T) {
	stk := testStoker()
	profile := testSyncProfile()
	injector := newInjector(stk, profile)

	pod := basePod(map[string]string{
		stokertypes.AnnotationInject:      "true",
		stokertypes.AnnotationCRName:      "my-sync",
		stokertypes.AnnotationSyncProfile: "my-profile",
		stokertypes.AnnotationGatewayName: "blue-gw",
	})
	resp := injector.Handle(context.Background(), makeAdmissionRequest(pod))

	if !resp.Allowed {
		t.Fatalf("expected allowed, got denied: %s", resp.Result.Message)
	}
	if resp.Patches == nil {
		t.Fatal("expected patches, got nil")
	}

	// Verify mutation content via direct injection
	patched := injectDirect(t, pod, stk)
	assertHasInitContainer(t, patched, agentContainerName)
	assertHasVolume(t, patched, volumeSyncRepo)
	assertHasVolume(t, patched, volumeGitCredentials)
	assertHasVolume(t, patched, volumeAPIKey)
	assertAnnotation(t, patched, stokertypes.AnnotationInjected, "true")
}

func TestInject_WithoutInjectAnnotation(t *testing.T) {
	injector := newInjector(testStoker())

	pod := basePod(map[string]string{})
	resp := injector.Handle(context.Background(), makeAdmissionRequest(pod))

	if !resp.Allowed {
		t.Fatalf("expected allowed, got denied: %s", resp.Result.Message)
	}
	if resp.Patches != nil {
		t.Fatal("expected no patches for non-annotated pod")
	}
}

func TestInject_MissingCR(t *testing.T) {
	injector := newInjector() // no CRs

	pod := basePod(map[string]string{
		stokertypes.AnnotationInject: "true",
		stokertypes.AnnotationCRName: "nonexistent",
	})
	resp := injector.Handle(context.Background(), makeAdmissionRequest(pod))

	if resp.Allowed {
		t.Fatal("expected denied for missing CR")
	}
	assertContains(t, resp.Result.Message, "not found")
}

func TestInject_PausedCR(t *testing.T) {
	stk := testStoker()
	stk.Spec.Paused = true
	injector := newInjector(stk)

	pod := basePod(map[string]string{
		stokertypes.AnnotationInject: "true",
		stokertypes.AnnotationCRName: "my-sync",
	})
	resp := injector.Handle(context.Background(), makeAdmissionRequest(pod))

	if resp.Allowed {
		t.Fatal("expected denied for paused CR")
	}
	assertContains(t, resp.Result.Message, "paused")
}

func TestInject_InvalidSyncProfile(t *testing.T) {
	injector := newInjector(testStoker()) // no profile

	pod := basePod(map[string]string{
		stokertypes.AnnotationInject:      "true",
		stokertypes.AnnotationCRName:      "my-sync",
		stokertypes.AnnotationSyncProfile: "nonexistent-profile",
	})
	resp := injector.Handle(context.Background(), makeAdmissionRequest(pod))

	if resp.Allowed {
		t.Fatal("expected denied for missing SyncProfile")
	}
	assertContains(t, resp.Result.Message, "not found")
}

func TestInject_AlreadyInjected(t *testing.T) {
	injector := newInjector(testStoker())

	restartAlways := corev1.ContainerRestartPolicyAlways
	pod := basePod(map[string]string{
		stokertypes.AnnotationInject: "true",
		stokertypes.AnnotationCRName: "my-sync",
	})
	pod.Spec.InitContainers = []corev1.Container{
		{Name: agentContainerName, Image: "test:latest", RestartPolicy: &restartAlways},
	}
	resp := injector.Handle(context.Background(), makeAdmissionRequest(pod))

	if !resp.Allowed {
		t.Fatalf("expected allowed for already injected, got: %s", resp.Result.Message)
	}
	if resp.Patches != nil {
		t.Fatal("expected no patches for already injected pod")
	}
}

func TestInject_AutoDeriveCRName_SingleCR(t *testing.T) {
	injector := newInjector(testStoker())

	pod := basePod(map[string]string{
		stokertypes.AnnotationInject: "true",
		// No cr-name annotation — should auto-derive
	})
	resp := injector.Handle(context.Background(), makeAdmissionRequest(pod))

	if !resp.Allowed {
		t.Fatalf("expected allowed with auto-derived CR, got: %s", resp.Result.Message)
	}
	if resp.Patches == nil {
		t.Fatal("expected patches for auto-derived injection")
	}
}

func TestInject_AutoDeriveCRName_NoCRs(t *testing.T) {
	injector := newInjector() // no CRs

	pod := basePod(map[string]string{
		stokertypes.AnnotationInject: "true",
	})
	resp := injector.Handle(context.Background(), makeAdmissionRequest(pod))

	if resp.Allowed {
		t.Fatal("expected denied when no CRs in namespace")
	}
	assertContains(t, resp.Result.Message, "no Stoker CR found")
}

func TestInject_AutoDeriveCRName_MultipleCRs(t *testing.T) {
	cr1 := testStoker()
	cr2 := testStoker()
	cr2.Name = "other-sync"
	injector := newInjector(cr1, cr2)

	pod := basePod(map[string]string{
		stokertypes.AnnotationInject: "true",
	})
	resp := injector.Handle(context.Background(), makeAdmissionRequest(pod))

	if resp.Allowed {
		t.Fatal("expected denied with multiple CRs")
	}
	assertContains(t, resp.Result.Message, "multiple Stoker CRs")
}

func TestInject_SSHAuth(t *testing.T) {
	stk := testStoker()
	stk.Spec.Git.Auth = &stokerv1alpha1.GitAuthSpec{
		SSHKey: &stokerv1alpha1.SSHKeyAuth{
			SecretRef: stokerv1alpha1.SecretKeyRef{
				Name: "ssh-key-secret",
				Key:  "ssh-privatekey",
			},
		},
	}
	injector := newInjector(stk)

	pod := basePod(map[string]string{
		stokertypes.AnnotationInject: "true",
		stokertypes.AnnotationCRName: "my-sync",
	})
	resp := injector.Handle(context.Background(), makeAdmissionRequest(pod))

	if !resp.Allowed {
		t.Fatalf("expected allowed, got: %s", resp.Result.Message)
	}

	patched := injectDirect(t, pod, stk)
	agent := findInitContainer(patched)
	if agent == nil {
		t.Fatal("stoker-agent not found")
	}
	assertEnvVar(t, agent, "GIT_SSH_KEY_FILE", mountGitCredentials+"/ssh-privatekey")
	assertVolumeSecret(t, patched, volumeGitCredentials, "ssh-key-secret")
}

func TestInject_TokenAuth(t *testing.T) {
	stk := testStoker()
	injector := newInjector(stk)

	pod := basePod(map[string]string{
		stokertypes.AnnotationInject: "true",
		stokertypes.AnnotationCRName: "my-sync",
	})
	resp := injector.Handle(context.Background(), makeAdmissionRequest(pod))

	if !resp.Allowed {
		t.Fatalf("expected allowed, got: %s", resp.Result.Message)
	}

	patched := injectDirect(t, pod, stk)
	agent := findInitContainer(patched)
	if agent == nil {
		t.Fatal("stoker-agent not found")
	}
	assertEnvVar(t, agent, "GIT_TOKEN_FILE", mountGitCredentials+"/token")
	assertVolumeSecret(t, patched, volumeGitCredentials, "git-token-secret")
}

func TestInject_AgentImageOverrideViaAnnotation(t *testing.T) {
	stk := testStoker()
	injector := newInjector(stk)

	pod := basePod(map[string]string{
		stokertypes.AnnotationInject:     "true",
		stokertypes.AnnotationCRName:     "my-sync",
		stokertypes.AnnotationAgentImage: "custom-registry.io/agent:debug",
	})
	resp := injector.Handle(context.Background(), makeAdmissionRequest(pod))

	if !resp.Allowed {
		t.Fatalf("expected allowed, got: %s", resp.Result.Message)
	}

	// Verify via direct injection — annotation image takes priority
	patched := pod.DeepCopy()
	injectSidecar(patched, stk)
	agent := findInitContainer(patched)
	if agent == nil {
		t.Fatal("stoker-agent not found")
	}
	if agent.Image != "custom-registry.io/agent:debug" {
		t.Fatalf("expected custom image, got %s", agent.Image)
	}
}

func TestInject_AgentResourcesFromCR(t *testing.T) {
	stk := testStoker()
	stk.Spec.Agent.Resources = &corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("100m"),
			corev1.ResourceMemory: resource.MustParse("128Mi"),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("500m"),
			corev1.ResourceMemory: resource.MustParse("512Mi"),
		},
	}
	injector := newInjector(stk)

	pod := basePod(map[string]string{
		stokertypes.AnnotationInject: "true",
		stokertypes.AnnotationCRName: "my-sync",
	})
	resp := injector.Handle(context.Background(), makeAdmissionRequest(pod))

	if !resp.Allowed {
		t.Fatalf("expected allowed, got: %s", resp.Result.Message)
	}

	patched := injectDirect(t, pod, stk)
	agent := findInitContainer(patched)
	if agent == nil {
		t.Fatal("stoker-agent not found")
	}

	cpuReq := agent.Resources.Requests[corev1.ResourceCPU]
	if cpuReq.String() != "100m" {
		t.Fatalf("expected CPU request 100m, got %s", cpuReq.String())
	}
	memLimit := agent.Resources.Limits[corev1.ResourceMemory]
	if memLimit.String() != "512Mi" {
		t.Fatalf("expected memory limit 512Mi, got %s", memLimit.String())
	}
}

// --- Helpers ---

// injectDirect calls injectSidecar on a pod copy with the given CR for testing.
func injectDirect(t *testing.T, pod *corev1.Pod, stk *stokerv1alpha1.Stoker) *corev1.Pod {
	t.Helper()
	p := pod.DeepCopy()
	injectSidecar(p, stk)
	return p
}

func assertHasInitContainer(t *testing.T, pod *corev1.Pod, name string) {
	t.Helper()
	for _, c := range pod.Spec.InitContainers {
		if c.Name == name {
			return
		}
	}
	t.Errorf("initContainer %q not found", name)
}

func assertHasVolume(t *testing.T, pod *corev1.Pod, name string) {
	t.Helper()
	for _, v := range pod.Spec.Volumes {
		if v.Name == name {
			return
		}
	}
	t.Errorf("volume %q not found", name)
}

func assertAnnotation(t *testing.T, pod *corev1.Pod, key, value string) {
	t.Helper()
	if pod.Annotations[key] != value {
		t.Errorf("annotation %s: expected %q, got %q", key, value, pod.Annotations[key])
	}
}

func findInitContainer(pod *corev1.Pod) *corev1.Container {
	for i, c := range pod.Spec.InitContainers {
		if c.Name == agentContainerName {
			return &pod.Spec.InitContainers[i]
		}
	}
	return nil
}

func assertEnvVar(t *testing.T, container *corev1.Container, name, expectedValue string) {
	t.Helper()
	for _, env := range container.Env {
		if env.Name == name {
			if env.Value != expectedValue {
				t.Errorf("env %s: expected %q, got %q", name, expectedValue, env.Value)
			}
			return
		}
	}
	t.Errorf("env %s not found", name)
}

func assertVolumeSecret(t *testing.T, pod *corev1.Pod, volumeName, secretName string) {
	t.Helper()
	for _, v := range pod.Spec.Volumes {
		if v.Name == volumeName && v.Secret != nil {
			if v.Secret.SecretName != secretName {
				t.Errorf("volume %s: expected secret %q, got %q", volumeName, secretName, v.Secret.SecretName)
			}
			return
		}
	}
	t.Errorf("volume %s not found or not a secret volume", volumeName)
}

func assertContains(t *testing.T, s, substr string) {
	t.Helper()
	if len(s) == 0 {
		t.Errorf("expected string containing %q, got empty string", substr)
		return
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return
		}
	}
	t.Errorf("expected %q to contain %q", s, substr)
}
