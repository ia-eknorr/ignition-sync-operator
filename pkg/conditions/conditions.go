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

package conditions

// Condition types for IgnitionSync status.conditions[].type
const (
	// TypeReady indicates overall readiness — all gateways synced and healthy.
	TypeReady = "Ready"

	// TypeRefResolved indicates whether the git ref has been resolved to a commit SHA.
	TypeRefResolved = "RefResolved"

	// TypeWebhookReady indicates whether the webhook listener is active.
	TypeWebhookReady = "WebhookReady"

	// TypeAllGatewaysSynced indicates whether all discovered gateways have completed sync.
	TypeAllGatewaysSynced = "AllGatewaysSynced"

	// TypeBidirectionalReady indicates whether bi-directional sync watchers are active.
	TypeBidirectionalReady = "BidirectionalReady"
)

// Condition reasons for IgnitionSync status.conditions[].reason
const (
	ReasonReconciling         = "Reconciling"
	ReasonRefResolved         = "RefResolved"
	ReasonRefResolutionFailed = "RefResolutionFailed"
	ReasonSyncSucceeded       = "SyncSucceeded"
	ReasonSyncFailed          = "SyncFailed"
	ReasonSyncInProgress      = "SyncInProgress"
	ReasonPaused              = "Paused"
	ReasonNoGateways          = "NoGatewaysDiscovered"
)
