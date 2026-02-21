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
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

const testHMACSecret = "my-secret-key"

func computeHMAC(payload []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestValidateHMAC_Valid(t *testing.T) {
	payload := []byte(`{"ref":"v1.0.0"}`)
	signature := computeHMAC(payload, testHMACSecret)

	if err := ValidateHMAC(payload, signature, testHMACSecret); err != nil {
		t.Fatalf("expected valid HMAC, got error: %v", err)
	}
}

func TestValidateHMAC_Invalid(t *testing.T) {
	payload := []byte(`{"ref":"v1.0.0"}`)
	if err := ValidateHMAC(payload, "sha256=deadbeef", testHMACSecret); err == nil {
		t.Fatal("expected error for invalid HMAC")
	}
}

func TestValidateHMAC_MissingPrefix(t *testing.T) {
	payload := []byte(`{"ref":"v1.0.0"}`)
	mac := hmac.New(sha256.New, []byte(testHMACSecret))
	mac.Write(payload)
	bare := hex.EncodeToString(mac.Sum(nil))

	if err := ValidateHMAC(payload, bare, testHMACSecret); err == nil {
		t.Fatal("expected error for missing sha256= prefix")
	}
}

func TestValidateHMAC_EmptySecret(t *testing.T) {
	payload := []byte(`{"ref":"v1.0.0"}`)

	if err := ValidateHMAC(payload, "sha256=abc", ""); err == nil {
		t.Fatal("expected error for empty secret")
	}
}
