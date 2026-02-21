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

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Println("ignition-sync-agent starting...")
	// TODO: implement agent — see docs/architecture/06a-agent-development-plan.md
	// Watches metadata ConfigMap for sync signals, clones repo to local emptyDir,
	// builds staging from SyncProfile mappings, merges to /ignition-data/,
	// triggers Ignition scan API, reports status via ConfigMap.
	fmt.Println("agent not yet implemented")
	os.Exit(0)
}
