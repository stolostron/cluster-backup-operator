/*
Copyright 2021.

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

package tlsconfig

import (
	"crypto/tls"
	"testing"

	ocinfrav1 "github.com/openshift/api/config/v1"
	openshifttls "github.com/openshift/controller-runtime-common/pkg/tls"
	ctrl "sigs.k8s.io/controller-runtime"
)

func TestGetDefaultTLSProfile(t *testing.T) {
	profile := GetDefaultTLSProfile()

	// Verify the default profile uses Intermediate settings
	if profile.MinTLSVersion != openshifttls.DefaultMinTLSVersion {
		t.Errorf("GetDefaultTLSProfile() MinTLSVersion = %v, want %v",
			profile.MinTLSVersion, openshifttls.DefaultMinTLSVersion)
	}

	if len(profile.Ciphers) == 0 {
		t.Error("GetDefaultTLSProfile() Ciphers should not be empty")
	}

	// Verify it matches the expected Intermediate profile ciphers
	if len(profile.Ciphers) != len(openshifttls.DefaultTLSCiphers) {
		t.Errorf("GetDefaultTLSProfile() Ciphers length = %d, want %d",
			len(profile.Ciphers), len(openshifttls.DefaultTLSCiphers))
	}
}

func TestBuildTLSConfig_DisablesHTTP2ByDefault(t *testing.T) {
	logger := ctrl.Log.WithName("test")
	profile := GetDefaultTLSProfile()

	// Build config with HTTP/2 disabled (default)
	config := BuildTLSConfig(profile, false, logger)

	if config == nil {
		t.Fatal("BuildTLSConfig() returned nil")
	}

	if len(config.TLSOpts) < 2 {
		t.Errorf("BuildTLSConfig() should have at least 2 TLS options (HTTP/2 disable + profile), got %d",
			len(config.TLSOpts))
	}

	// Apply the options to a tls.Config and verify HTTP/2 is disabled
	tlsConfig := &tls.Config{}
	ApplyTLSOptions(config.TLSOpts, tlsConfig)

	// When HTTP/2 is disabled, NextProtos should be ["http/1.1"]
	if len(tlsConfig.NextProtos) != 1 || tlsConfig.NextProtos[0] != "http/1.1" {
		t.Errorf("BuildTLSConfig() with HTTP/2 disabled should set NextProtos to [http/1.1], got %v",
			tlsConfig.NextProtos)
	}
}

func TestBuildTLSConfig_EnablesHTTP2WhenRequested(t *testing.T) {
	logger := ctrl.Log.WithName("test")
	profile := GetDefaultTLSProfile()

	// Build config with HTTP/2 enabled
	config := BuildTLSConfig(profile, true, logger)

	if config == nil {
		t.Fatal("BuildTLSConfig() returned nil")
	}

	// Apply the options to a tls.Config
	tlsConfig := &tls.Config{}
	ApplyTLSOptions(config.TLSOpts, tlsConfig)

	// When HTTP/2 is enabled, NextProtos should be empty (letting Go handle default negotiation)
	// or explicitly include "h2". It should NOT be restricted to just "http/1.1".
	if len(tlsConfig.NextProtos) == 1 && tlsConfig.NextProtos[0] == "http/1.1" {
		t.Error("BuildTLSConfig() with HTTP/2 enabled should not restrict to http/1.1 only")
	}
}

func TestBuildTLSConfig_AppliesMinTLSVersion(t *testing.T) {
	logger := ctrl.Log.WithName("test")

	tests := []struct {
		name               string
		minTLSVersion      ocinfrav1.TLSProtocolVersion
		expectedMinVersion uint16
	}{
		{
			name:               "TLS 1.2",
			minTLSVersion:      ocinfrav1.VersionTLS12,
			expectedMinVersion: tls.VersionTLS12,
		},
		{
			name:               "TLS 1.3",
			minTLSVersion:      ocinfrav1.VersionTLS13,
			expectedMinVersion: tls.VersionTLS13,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profile := ocinfrav1.TLSProfileSpec{
				MinTLSVersion: tt.minTLSVersion,
				Ciphers:       openshifttls.DefaultTLSCiphers,
			}

			config := BuildTLSConfig(profile, true, logger)

			// Apply the options to a tls.Config
			tlsConfig := &tls.Config{}
			ApplyTLSOptions(config.TLSOpts, tlsConfig)

			if tlsConfig.MinVersion != tt.expectedMinVersion {
				t.Errorf("BuildTLSConfig() MinVersion = %v, want %v",
					tlsConfig.MinVersion, tt.expectedMinVersion)
			}
		})
	}
}

func TestBuildTLSConfig_AppliesCipherSuites(t *testing.T) {
	logger := ctrl.Log.WithName("test")

	// Use TLS 1.2 profile to test cipher suites (TLS 1.3 doesn't allow configuring ciphers)
	profile := ocinfrav1.TLSProfileSpec{
		MinTLSVersion: ocinfrav1.VersionTLS12,
		Ciphers:       openshifttls.DefaultTLSCiphers,
	}

	config := BuildTLSConfig(profile, true, logger)

	// Apply the options to a tls.Config
	tlsConfig := &tls.Config{}
	ApplyTLSOptions(config.TLSOpts, tlsConfig)

	// For TLS 1.2, cipher suites should be configured
	if len(tlsConfig.CipherSuites) == 0 {
		t.Error("BuildTLSConfig() with TLS 1.2 should configure CipherSuites")
	}
}

func TestBuildTLSConfig_TLS13DoesNotConfigureCipherSuites(t *testing.T) {
	logger := ctrl.Log.WithName("test")

	// TLS 1.3 doesn't allow configuring cipher suites in Go
	profile := ocinfrav1.TLSProfileSpec{
		MinTLSVersion: ocinfrav1.VersionTLS13,
		Ciphers:       []string{}, // Empty for TLS 1.3
	}

	config := BuildTLSConfig(profile, true, logger)

	// Apply the options to a tls.Config
	tlsConfig := &tls.Config{}
	ApplyTLSOptions(config.TLSOpts, tlsConfig)

	// For TLS 1.3, cipher suites should NOT be configured (Go manages them)
	if len(tlsConfig.CipherSuites) != 0 {
		t.Errorf("BuildTLSConfig() with TLS 1.3 should not configure CipherSuites, got %v",
			tlsConfig.CipherSuites)
	}
}

func TestBuildTLSConfig_ReturnsUnsupportedCiphers(t *testing.T) {
	logger := ctrl.Log.WithName("test")

	// Include an unsupported cipher
	profile := ocinfrav1.TLSProfileSpec{
		MinTLSVersion: ocinfrav1.VersionTLS12,
		Ciphers: []string{
			"ECDHE-RSA-AES128-GCM-SHA256", // Supported
			"FAKE-CIPHER-NOT-REAL",        // Not supported
		},
	}

	config := BuildTLSConfig(profile, true, logger)

	// Should report the unsupported cipher
	if len(config.UnsupportedCiphers) == 0 {
		t.Error("BuildTLSConfig() should report unsupported ciphers")
	}

	found := false
	for _, cipher := range config.UnsupportedCiphers {
		if cipher == "FAKE-CIPHER-NOT-REAL" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("BuildTLSConfig() UnsupportedCiphers should contain 'FAKE-CIPHER-NOT-REAL', got %v",
			config.UnsupportedCiphers)
	}
}

func TestApplyTLSOptions(t *testing.T) {
	// Test that ApplyTLSOptions correctly applies multiple options
	tlsConfig := &tls.Config{}

	opts := []func(*tls.Config){
		func(c *tls.Config) {
			c.MinVersion = tls.VersionTLS12
		},
		func(c *tls.Config) {
			c.NextProtos = []string{"http/1.1"}
		},
	}

	ApplyTLSOptions(opts, tlsConfig)

	if tlsConfig.MinVersion != tls.VersionTLS12 {
		t.Errorf("ApplyTLSOptions() MinVersion = %v, want %v", tlsConfig.MinVersion, tls.VersionTLS12)
	}

	if len(tlsConfig.NextProtos) != 1 || tlsConfig.NextProtos[0] != "http/1.1" {
		t.Errorf("ApplyTLSOptions() NextProtos = %v, want [http/1.1]", tlsConfig.NextProtos)
	}
}

func TestGetTLSProfileType(t *testing.T) {
	tests := []struct {
		name     string
		profile  ocinfrav1.TLSProfileSpec
		expected string
	}{
		{
			name:     "Intermediate profile",
			profile:  *ocinfrav1.TLSProfiles[ocinfrav1.TLSProfileIntermediateType],
			expected: string(ocinfrav1.TLSProfileIntermediateType),
		},
		{
			name:     "Modern profile",
			profile:  *ocinfrav1.TLSProfiles[ocinfrav1.TLSProfileModernType],
			expected: string(ocinfrav1.TLSProfileModernType),
		},
		{
			name: "Custom profile",
			profile: ocinfrav1.TLSProfileSpec{
				MinTLSVersion: ocinfrav1.VersionTLS12,
				Ciphers:       []string{"ECDHE-RSA-AES128-GCM-SHA256"},
			},
			expected: "Custom",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetTLSProfileType(tt.profile)
			if result != tt.expected {
				t.Errorf("GetTLSProfileType() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestConfig_TLSProfileSpecIsPreserved(t *testing.T) {
	logger := ctrl.Log.WithName("test")

	profile := ocinfrav1.TLSProfileSpec{
		MinTLSVersion: ocinfrav1.VersionTLS13,
		Ciphers:       []string{},
	}

	config := BuildTLSConfig(profile, false, logger)

	// Verify the profile spec is preserved in the config
	if config.TLSProfileSpec.MinTLSVersion != profile.MinTLSVersion {
		t.Errorf("BuildTLSConfig() TLSProfileSpec.MinTLSVersion = %v, want %v",
			config.TLSProfileSpec.MinTLSVersion, profile.MinTLSVersion)
	}
}

func TestProfilesMatch_IdenticalProfiles(t *testing.T) {
	a := ocinfrav1.TLSProfileSpec{
		MinTLSVersion: ocinfrav1.VersionTLS12,
		Ciphers:       []string{"ECDHE-RSA-AES128-GCM-SHA256", "ECDHE-RSA-AES256-GCM-SHA384"},
	}
	b := ocinfrav1.TLSProfileSpec{
		MinTLSVersion: ocinfrav1.VersionTLS12,
		Ciphers:       []string{"ECDHE-RSA-AES128-GCM-SHA256", "ECDHE-RSA-AES256-GCM-SHA384"},
	}
	if !profilesMatch(a, b) {
		t.Error("profilesMatch() should return true for identical profiles")
	}
}

func TestProfilesMatch_DifferentTLSVersions(t *testing.T) {
	a := ocinfrav1.TLSProfileSpec{
		MinTLSVersion: ocinfrav1.VersionTLS12,
		Ciphers:       []string{"ECDHE-RSA-AES128-GCM-SHA256"},
	}
	b := ocinfrav1.TLSProfileSpec{
		MinTLSVersion: ocinfrav1.VersionTLS13,
		Ciphers:       []string{"ECDHE-RSA-AES128-GCM-SHA256"},
	}
	if profilesMatch(a, b) {
		t.Error("profilesMatch() should return false for different TLS versions")
	}
}

func TestProfilesMatch_DifferentCipherCount(t *testing.T) {
	a := ocinfrav1.TLSProfileSpec{
		MinTLSVersion: ocinfrav1.VersionTLS12,
		Ciphers:       []string{"ECDHE-RSA-AES128-GCM-SHA256"},
	}
	b := ocinfrav1.TLSProfileSpec{
		MinTLSVersion: ocinfrav1.VersionTLS12,
		Ciphers:       []string{"ECDHE-RSA-AES128-GCM-SHA256", "ECDHE-RSA-AES256-GCM-SHA384"},
	}
	if profilesMatch(a, b) {
		t.Error("profilesMatch() should return false for different cipher count")
	}
}

func TestProfilesMatch_SameCiphersDifferentOrder(t *testing.T) {
	a := ocinfrav1.TLSProfileSpec{
		MinTLSVersion: ocinfrav1.VersionTLS12,
		Ciphers:       []string{"ECDHE-RSA-AES128-GCM-SHA256", "ECDHE-RSA-AES256-GCM-SHA384"},
	}
	b := ocinfrav1.TLSProfileSpec{
		MinTLSVersion: ocinfrav1.VersionTLS12,
		Ciphers:       []string{"ECDHE-RSA-AES256-GCM-SHA384", "ECDHE-RSA-AES128-GCM-SHA256"},
	}
	if profilesMatch(a, b) {
		t.Error("profilesMatch() should return false for same ciphers in different order")
	}
}

func TestProfilesMatch_BothEmptyCiphers(t *testing.T) {
	a := ocinfrav1.TLSProfileSpec{
		MinTLSVersion: ocinfrav1.VersionTLS13,
		Ciphers:       []string{},
	}
	b := ocinfrav1.TLSProfileSpec{
		MinTLSVersion: ocinfrav1.VersionTLS13,
		Ciphers:       []string{},
	}
	if !profilesMatch(a, b) {
		t.Error("profilesMatch() should return true for both empty ciphers")
	}
}

func TestProfilesMatch_OneEmptyCiphers(t *testing.T) {
	a := ocinfrav1.TLSProfileSpec{
		MinTLSVersion: ocinfrav1.VersionTLS12,
		Ciphers:       []string{},
	}
	b := ocinfrav1.TLSProfileSpec{
		MinTLSVersion: ocinfrav1.VersionTLS12,
		Ciphers:       []string{"ECDHE-RSA-AES128-GCM-SHA256"},
	}
	if profilesMatch(a, b) {
		t.Error("profilesMatch() should return false when one has empty ciphers")
	}
}

func TestProfilesMatch_NilVsEmptyCiphers(t *testing.T) {
	a := ocinfrav1.TLSProfileSpec{
		MinTLSVersion: ocinfrav1.VersionTLS13,
		Ciphers:       nil,
	}
	b := ocinfrav1.TLSProfileSpec{
		MinTLSVersion: ocinfrav1.VersionTLS13,
		Ciphers:       []string{},
	}
	if !profilesMatch(a, b) {
		t.Error("profilesMatch() should return true for nil vs empty ciphers")
	}
}

func TestGetTLSProfileType_OldProfile(t *testing.T) {
	// Test the Old profile type which was not covered before
	oldProfile := ocinfrav1.TLSProfiles[ocinfrav1.TLSProfileOldType]
	if oldProfile == nil {
		t.Skip("Old TLS profile not defined in OpenShift API")
	}

	result := GetTLSProfileType(*oldProfile)
	if result != string(ocinfrav1.TLSProfileOldType) {
		t.Errorf("GetTLSProfileType() for Old profile = %v, want %v",
			result, string(ocinfrav1.TLSProfileOldType))
	}
}

func TestApplyTLSOptions_EmptyOptions(t *testing.T) {
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}

	// Apply empty options - should not panic or modify config
	ApplyTLSOptions([]func(*tls.Config){}, tlsConfig)

	// Config should remain unchanged
	if tlsConfig.MinVersion != tls.VersionTLS12 {
		t.Errorf("ApplyTLSOptions() with empty options should not modify config, MinVersion = %v",
			tlsConfig.MinVersion)
	}
}

func TestApplyTLSOptions_NilOptions(t *testing.T) {
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}

	// Apply nil options - should not panic
	ApplyTLSOptions(nil, tlsConfig)

	// Config should remain unchanged
	if tlsConfig.MinVersion != tls.VersionTLS12 {
		t.Errorf("ApplyTLSOptions() with nil options should not modify config, MinVersion = %v",
			tlsConfig.MinVersion)
	}
}

func TestBuildTLSConfig_EmptyCiphersForTLS12(t *testing.T) {
	logger := ctrl.Log.WithName("test")

	// TLS 1.2 with empty ciphers - unusual but should not panic
	profile := ocinfrav1.TLSProfileSpec{
		MinTLSVersion: ocinfrav1.VersionTLS12,
		Ciphers:       []string{},
	}

	config := BuildTLSConfig(profile, true, logger)

	if config == nil {
		t.Fatal("BuildTLSConfig() returned nil")
	}

	// Apply the options to a tls.Config
	tlsConfig := &tls.Config{}
	ApplyTLSOptions(config.TLSOpts, tlsConfig)

	// MinVersion should still be set correctly
	if tlsConfig.MinVersion != tls.VersionTLS12 {
		t.Errorf("BuildTLSConfig() MinVersion = %v, want %v",
			tlsConfig.MinVersion, tls.VersionTLS12)
	}
}

func TestGetDefaultTLSProfile_CipherContent(t *testing.T) {
	profile := GetDefaultTLSProfile()

	// Verify the actual cipher content matches, not just the length
	for i, cipher := range profile.Ciphers {
		if cipher != openshifttls.DefaultTLSCiphers[i] {
			t.Errorf("GetDefaultTLSProfile() Ciphers[%d] = %v, want %v",
				i, cipher, openshifttls.DefaultTLSCiphers[i])
		}
	}
}

func TestGetTLSProfileType_EmptyProfile(t *testing.T) {
	// Empty profile should return Custom
	emptyProfile := ocinfrav1.TLSProfileSpec{}

	result := GetTLSProfileType(emptyProfile)
	if result != "Custom" {
		t.Errorf("GetTLSProfileType() for empty profile = %v, want Custom", result)
	}
}

func TestBuildTLSConfig_PreservesAllFields(t *testing.T) {
	logger := ctrl.Log.WithName("test")

	ciphers := []string{"ECDHE-RSA-AES128-GCM-SHA256", "ECDHE-RSA-AES256-GCM-SHA384"}
	profile := ocinfrav1.TLSProfileSpec{
		MinTLSVersion: ocinfrav1.VersionTLS12,
		Ciphers:       ciphers,
	}

	config := BuildTLSConfig(profile, false, logger)

	// Verify TLSProfileSpec is fully preserved
	if config.TLSProfileSpec.MinTLSVersion != profile.MinTLSVersion {
		t.Errorf("BuildTLSConfig() TLSProfileSpec.MinTLSVersion = %v, want %v",
			config.TLSProfileSpec.MinTLSVersion, profile.MinTLSVersion)
	}

	if len(config.TLSProfileSpec.Ciphers) != len(profile.Ciphers) {
		t.Errorf("BuildTLSConfig() TLSProfileSpec.Ciphers length = %d, want %d",
			len(config.TLSProfileSpec.Ciphers), len(profile.Ciphers))
	}

	for i, cipher := range config.TLSProfileSpec.Ciphers {
		if cipher != profile.Ciphers[i] {
			t.Errorf("BuildTLSConfig() TLSProfileSpec.Ciphers[%d] = %v, want %v",
				i, cipher, profile.Ciphers[i])
		}
	}
}
