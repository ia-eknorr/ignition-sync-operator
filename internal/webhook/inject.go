package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	syncv1alpha1 "github.com/ia-eknorr/ignition-sync-operator/api/v1alpha1"
	synctypes "github.com/ia-eknorr/ignition-sync-operator/pkg/types"
)

const (
	agentContainerName = "sync-agent"

	defaultAgentImage = "ghcr.io/ia-eknorr/ignition-sync-agent:latest"

	// Volume names injected by the webhook.
	volumeSyncRepo       = "sync-repo"
	volumeGitCredentials = "git-credentials"
	volumeAPIKey         = "api-key"

	// Mount paths inside the agent container.
	mountRepo           = "/repo"
	mountIgnitionData   = "/ignition-data"
	mountGitCredentials = "/etc/ignition-sync/git-credentials"
	mountAPIKey         = "/etc/ignition-sync/api-key"

	// Environment variable for operator-level default agent image.
	envDefaultAgentImage = "DEFAULT_AGENT_IMAGE"

	// annotationTrue is the canonical "true" value for boolean annotations.
	annotationTrue = "true"
)

// PodInjector implements admission.Handler for sidecar injection.
type PodInjector struct {
	Client  client.Client
	Decoder admission.Decoder
}

// Handle processes admission requests for pod creation and injects the sync-agent sidecar.
func (p *PodInjector) Handle(ctx context.Context, req admission.Request) admission.Response {
	log := logf.FromContext(ctx).WithName("pod-injector")

	pod := &corev1.Pod{}
	if err := p.Decoder.Decode(req, pod); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	// Early return for non-annotated pods (~1ms, no network calls)
	if pod.Annotations[synctypes.AnnotationInject] != annotationTrue {
		return admission.Allowed("injection not requested")
	}

	// Idempotency: skip if already injected
	if isAlreadyInjected(pod) {
		return admission.Allowed("already injected")
	}

	// Resolve CR name (annotation or auto-derive)
	crName, err := p.resolveCRName(ctx, req.Namespace, pod)
	if err != nil {
		log.Info("denied injection", "pod", pod.Name, "reason", err.Error())
		return admission.Denied(err.Error())
	}

	// Fetch IgnitionSync CR
	var isync syncv1alpha1.IgnitionSync
	key := client.ObjectKey{Name: crName, Namespace: req.Namespace}
	if err := p.Client.Get(ctx, key, &isync); err != nil {
		if apierrors.IsNotFound(err) {
			return admission.Denied(fmt.Sprintf(
				"IgnitionSync CR '%s' not found in namespace '%s'", crName, req.Namespace))
		}
		return admission.Errored(http.StatusInternalServerError, err)
	}

	// Check if CR is paused
	if isync.Spec.Paused {
		return admission.Denied(fmt.Sprintf(
			"IgnitionSync CR '%s' is paused", crName))
	}

	// Validate SyncProfile if specified
	profileName := pod.Annotations[synctypes.AnnotationSyncProfile]
	if profileName != "" {
		var profile syncv1alpha1.SyncProfile
		profileKey := client.ObjectKey{Name: profileName, Namespace: req.Namespace}
		if err := p.Client.Get(ctx, profileKey, &profile); err != nil {
			if apierrors.IsNotFound(err) {
				return admission.Denied(fmt.Sprintf(
					"SyncProfile '%s' not found in namespace '%s'", profileName, req.Namespace))
			}
			return admission.Errored(http.StatusInternalServerError, err)
		}
	}

	// Inject sidecar
	injectSidecar(pod, &isync)

	// Return JSON patch
	marshaledPod, err := json.Marshal(pod)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}

	log.Info("injected sync-agent sidecar", "pod", pod.Name, "cr", crName, "namespace", req.Namespace)
	return admission.PatchResponseFromRaw(req.Object.Raw, marshaledPod)
}

// isAlreadyInjected checks if the sync-agent container already exists.
func isAlreadyInjected(pod *corev1.Pod) bool {
	for _, c := range pod.Spec.InitContainers {
		if c.Name == agentContainerName {
			return true
		}
	}
	return false
}

// resolveCRName resolves the IgnitionSync CR name from annotation or auto-derives it.
func (p *PodInjector) resolveCRName(ctx context.Context, namespace string, pod *corev1.Pod) (string, error) {
	if crName := pod.Annotations[synctypes.AnnotationCRName]; crName != "" {
		return crName, nil
	}

	// Auto-discover: list CRs in namespace
	var list syncv1alpha1.IgnitionSyncList
	if err := p.Client.List(ctx, &list, client.InNamespace(namespace)); err != nil {
		return "", fmt.Errorf("failed to list IgnitionSync CRs: %w", err)
	}

	switch len(list.Items) {
	case 0:
		return "", fmt.Errorf("no IgnitionSync CR found in namespace '%s'", namespace)
	case 1:
		return list.Items[0].Name, nil
	default:
		names := make([]string, len(list.Items))
		for i, item := range list.Items {
			names[i] = item.Name
		}
		return "", fmt.Errorf(
			"multiple IgnitionSync CRs in namespace '%s': [%s] — set annotation '%s' explicitly",
			namespace, strings.Join(names, ", "), synctypes.AnnotationCRName)
	}
}

// injectSidecar patches the pod spec with the sync-agent native sidecar.
func injectSidecar(pod *corev1.Pod, isync *syncv1alpha1.IgnitionSync) {
	image := resolveAgentImage(pod, isync)
	pullPolicy := resolveAgentPullPolicy(isync)

	// Determine gateway name for env var
	gatewayName := pod.Annotations[synctypes.AnnotationGatewayName]

	// Determine sync profile
	syncProfile := pod.Annotations[synctypes.AnnotationSyncProfile]

	// Determine CR name — use annotation if set, otherwise use isync.Name
	crName := pod.Annotations[synctypes.AnnotationCRName]
	if crName == "" {
		crName = isync.Name
	}

	// Gateway port and TLS from CR
	gatewayPort := fmt.Sprintf("%d", isync.Spec.Gateway.Port)
	if isync.Spec.Gateway.Port == 0 {
		gatewayPort = "8043"
	}
	gatewayTLS := annotationTrue
	if isync.Spec.Gateway.TLS != nil && !*isync.Spec.Gateway.TLS {
		gatewayTLS = "false"
	}

	// Build env vars
	env := buildEnvVars(crName, gatewayName, syncProfile, gatewayPort, gatewayTLS, isync)

	// Build resources
	resources := buildResources(isync)

	// Security context (restricted PSS).
	// We intentionally omit RunAsUser so the agent inherits the pod-level
	// security context. This ensures files written to the shared data volume
	// are owned by the same UID as the gateway container (e.g. 2003 for
	// Ignition helm chart pods), preventing permission errors.
	restartAlways := corev1.ContainerRestartPolicyAlways
	agentContainer := corev1.Container{
		Name:            agentContainerName,
		Image:           image,
		ImagePullPolicy: corev1.PullPolicy(pullPolicy),
		Command:         []string{"/agent"},
		RestartPolicy:   &restartAlways,
		Env:             env,
		Resources:       resources,
		SecurityContext: &corev1.SecurityContext{
			RunAsNonRoot:             boolPtr(true),
			ReadOnlyRootFilesystem:   boolPtr(true),
			AllowPrivilegeEscalation: boolPtr(false),
			SeccompProfile: &corev1.SeccompProfile{
				Type: corev1.SeccompProfileTypeRuntimeDefault,
			},
			Capabilities: &corev1.Capabilities{
				Drop: []corev1.Capability{"ALL"},
			},
		},
		StartupProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path: "/startupz",
					Port: intstr.FromInt32(8082),
				},
			},
			PeriodSeconds:    2,
			FailureThreshold: 30,
		},
		VolumeMounts: []corev1.VolumeMount{
			{Name: volumeSyncRepo, MountPath: mountRepo},
			{Name: volumeGitCredentials, MountPath: mountGitCredentials, ReadOnly: true},
			{Name: volumeAPIKey, MountPath: mountAPIKey, ReadOnly: true},
		},
	}

	// Mount the Ignition data volume if it exists on the pod.
	// Try well-known names: "ignition-data" (explicit) or "data" (Ignition helm chart PVC).
	// When found, discover the mount path from existing containers to set DATA_PATH correctly.
	dataVolName, dataPath := resolveDataVolume(pod)
	if dataVolName != "" {
		agentContainer.VolumeMounts = append(agentContainer.VolumeMounts, corev1.VolumeMount{
			Name:      dataVolName,
			MountPath: dataPath,
		})
		// Override DATA_PATH env var to match the actual mount
		for i := range agentContainer.Env {
			if agentContainer.Env[i].Name == "DATA_PATH" {
				agentContainer.Env[i].Value = dataPath
				break
			}
		}
	}

	// Prepend as native sidecar (initContainer with restartPolicy: Always)
	pod.Spec.InitContainers = append([]corev1.Container{agentContainer}, pod.Spec.InitContainers...)

	// Add volumes
	secretMode := int32(0400)
	newVolumes := []corev1.Volume{
		{
			Name: volumeSyncRepo,
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
		{
			Name: volumeGitCredentials,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName:  gitCredentialSecretName(isync),
					DefaultMode: &secretMode,
				},
			},
		},
		{
			Name: volumeAPIKey,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName:  isync.Spec.Gateway.APIKeySecretRef.Name,
					DefaultMode: &secretMode,
				},
			},
		},
	}
	pod.Spec.Volumes = append(pod.Spec.Volumes, newVolumes...)

	// Set injected annotation
	if pod.Annotations == nil {
		pod.Annotations = make(map[string]string)
	}
	pod.Annotations[synctypes.AnnotationInjected] = annotationTrue
}

// resolveAgentImage resolves the agent image using 3-tier priority:
// 1. Pod annotation (debugging override)
// 2. CR spec.agent.image
// 3. Environment variable / hardcoded default
func resolveAgentImage(pod *corev1.Pod, isync *syncv1alpha1.IgnitionSync) string {
	// Tier 1: annotation override
	if img := pod.Annotations[synctypes.AnnotationAgentImage]; img != "" {
		return img
	}

	// Tier 2: CR spec
	spec := isync.Spec.Agent.Image
	if spec.Repository != "" {
		tag := spec.Tag
		if tag == "" {
			tag = "latest"
		}
		return spec.Repository + ":" + tag
	}

	// Tier 3: env var fallback
	if img := os.Getenv(envDefaultAgentImage); img != "" {
		return img
	}
	return defaultAgentImage
}

// resolveAgentPullPolicy returns the image pull policy from CR spec or default.
func resolveAgentPullPolicy(isync *syncv1alpha1.IgnitionSync) string {
	if p := isync.Spec.Agent.Image.PullPolicy; p != "" {
		return p
	}
	return "IfNotPresent"
}

// buildEnvVars constructs the environment variables for the agent container.
func buildEnvVars(crName, gatewayName, syncProfile, gatewayPort, gatewayTLS string, isync *syncv1alpha1.IgnitionSync) []corev1.EnvVar {
	env := []corev1.EnvVar{
		{
			Name: "POD_NAME",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.name"},
			},
		},
		{
			Name: "POD_NAMESPACE",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.namespace"},
			},
		},
		{Name: "CR_NAME", Value: crName},
		{
			Name: "CR_NAMESPACE",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.namespace"},
			},
		},
		{Name: "GATEWAY_NAME", Value: gatewayName},
		{Name: "SYNC_PROFILE", Value: syncProfile},
		{Name: "REPO_PATH", Value: mountRepo},
		{Name: "DATA_PATH", Value: mountIgnitionData},
		{Name: "GATEWAY_PORT", Value: gatewayPort},
		{Name: "GATEWAY_TLS", Value: gatewayTLS},
		{Name: "API_KEY_FILE", Value: mountAPIKey + "/" + isync.Spec.Gateway.APIKeySecretRef.Key},
	}

	// Git credential env vars depend on auth type
	if isync.Spec.Git.Auth != nil {
		if isync.Spec.Git.Auth.SSHKey != nil {
			env = append(env, corev1.EnvVar{
				Name:  "GIT_SSH_KEY_FILE",
				Value: mountGitCredentials + "/" + isync.Spec.Git.Auth.SSHKey.SecretRef.Key,
			})
		} else if isync.Spec.Git.Auth.Token != nil {
			env = append(env, corev1.EnvVar{
				Name:  "GIT_TOKEN_FILE",
				Value: mountGitCredentials + "/" + isync.Spec.Git.Auth.Token.SecretRef.Key,
			})
		}
	}

	// Sync period defaults to 30
	env = append(env, corev1.EnvVar{Name: "SYNC_PERIOD", Value: "30"})

	return env
}

// buildResources returns the agent container resources from CR spec or defaults.
func buildResources(isync *syncv1alpha1.IgnitionSync) corev1.ResourceRequirements {
	if isync.Spec.Agent.Resources != nil {
		return *isync.Spec.Agent.Resources
	}
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("50m"),
			corev1.ResourceMemory: resource.MustParse("64Mi"),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("200m"),
			corev1.ResourceMemory: resource.MustParse("256Mi"),
		},
	}
}

// gitCredentialSecretName returns the secret name for git credentials based on auth config.
func gitCredentialSecretName(isync *syncv1alpha1.IgnitionSync) string {
	if isync.Spec.Git.Auth == nil {
		return "git-credentials" // fallback name
	}
	if isync.Spec.Git.Auth.SSHKey != nil {
		return isync.Spec.Git.Auth.SSHKey.SecretRef.Name
	}
	if isync.Spec.Git.Auth.Token != nil {
		return isync.Spec.Git.Auth.Token.SecretRef.Name
	}
	if isync.Spec.Git.Auth.GitHubApp != nil {
		return isync.Spec.Git.Auth.GitHubApp.PrivateKeySecretRef.Name
	}
	return "git-credentials"
}

// resolveDataVolume finds the Ignition data volume on the pod and returns
// (volumeName, mountPath). It looks for "ignition-data" first, then "data"
// (standard Ignition helm chart PVC). The mount path is discovered from the
// first container that mounts the volume; falls back to /ignition-data.
func resolveDataVolume(pod *corev1.Pod) (string, string) {
	candidates := []string{"ignition-data", "data"}
	volumes := make(map[string]bool, len(pod.Spec.Volumes))
	for _, v := range pod.Spec.Volumes {
		volumes[v.Name] = true
	}

	for _, name := range candidates {
		if !volumes[name] {
			continue
		}
		// Find mount path from existing containers
		for _, c := range pod.Spec.Containers {
			for _, vm := range c.VolumeMounts {
				if vm.Name == name {
					return name, vm.MountPath
				}
			}
		}
		// Volume exists but not mounted — use default path
		return name, mountIgnitionData
	}
	return "", ""
}

func boolPtr(b bool) *bool {
	return &b
}
