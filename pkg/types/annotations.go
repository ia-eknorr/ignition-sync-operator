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

package types

const (
	// AnnotationPrefix is the base prefix for all ignition-sync annotations.
	AnnotationPrefix = "ignition-sync.io"

	// Pod annotations — set by users on gateway pods to trigger sidecar injection.

	// AnnotationInject enables sidecar injection when set to "true".
	AnnotationInject = AnnotationPrefix + "/inject"

	// AnnotationCRName identifies which IgnitionSync CR in this namespace to use.
	// Auto-derived if exactly one CR exists in the namespace.
	AnnotationCRName = AnnotationPrefix + "/cr-name"

	// AnnotationServicePath is the repo-relative path to this gateway's service directory.
	AnnotationServicePath = AnnotationPrefix + "/service-path"

	// AnnotationGatewayName overrides gateway identity (defaults to pod label app.kubernetes.io/name).
	AnnotationGatewayName = AnnotationPrefix + "/gateway-name"

	// AnnotationDeploymentMode selects the config resource overlay to apply (e.g., "prd-cloud").
	AnnotationDeploymentMode = AnnotationPrefix + "/deployment-mode"

	// AnnotationTagProvider sets the UDT tag provider destination (default: "default").
	AnnotationTagProvider = AnnotationPrefix + "/tag-provider"

	// AnnotationSyncPeriod sets the fallback poll interval in seconds (default: "30").
	AnnotationSyncPeriod = AnnotationPrefix + "/sync-period"

	// AnnotationExcludePatterns is a comma-separated list of exclude globs for this gateway.
	AnnotationExcludePatterns = AnnotationPrefix + "/exclude-patterns"

	// AnnotationSystemName overrides the systemName used in config normalization.
	AnnotationSystemName = AnnotationPrefix + "/system-name"

	// AnnotationSystemNameTemplate is a Go template for systemName (default: "{{.GatewayName}}").
	AnnotationSystemNameTemplate = AnnotationPrefix + "/system-name-template"

	// CR annotations — set by the webhook receiver on the IgnitionSync CR (not by users).

	// AnnotationRequestedRef is set by the webhook receiver to request a ref update.
	// The controller reads this and initiates a sync to the requested ref.
	AnnotationRequestedRef = AnnotationPrefix + "/requested-ref"

	// AnnotationRequestedAt records when the webhook request was received.
	AnnotationRequestedAt = AnnotationPrefix + "/requested-at"

	// AnnotationRequestedBy records the source of the webhook request (e.g., "argocd", "kargo", "github").
	AnnotationRequestedBy = AnnotationPrefix + "/requested-by"

	// Labels

	// LabelCRName is used on owned resources (PVCs, ConfigMaps) to identify the parent CR.
	LabelCRName = AnnotationPrefix + "/cr-name"

	// Finalizer

	// Finalizer is added to IgnitionSync CRs to ensure cleanup on deletion.
	Finalizer = AnnotationPrefix + "/finalizer"
)
