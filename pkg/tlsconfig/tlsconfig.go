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

// Package tlsconfig provides utilities for configuring TLS based on OpenShift TLS profiles.
package tlsconfig

import (
	"context"
	"crypto/tls"

	"github.com/go-logr/logr"
	ocinfrav1 "github.com/openshift/api/config/v1"
	openshifttls "github.com/openshift/controller-runtime-common/pkg/tls"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Config holds the TLS configuration for the webhook server.
type Config struct {
	// TLSProfileSpec is the TLS profile specification from the cluster.
	TLSProfileSpec ocinfrav1.TLSProfileSpec

	// TLSOpts are the TLS configuration functions to apply to the webhook server.
	TLSOpts []func(*tls.Config)

	// UnsupportedCiphers are cipher names from the profile that are not supported.
	UnsupportedCiphers []string
}

// FetchTLSProfile fetches the TLS profile from the APIServer configuration.
// Returns an error if the profile cannot be fetched (e.g., RBAC permission issues).
func FetchTLSProfile(ctx context.Context, k8sClient client.Client) (ocinfrav1.TLSProfileSpec, error) {
	tlsProfileSpec, err := openshifttls.FetchAPIServerTLSProfile(ctx, k8sClient)
	if err != nil {
		return ocinfrav1.TLSProfileSpec{}, err
	}

	return tlsProfileSpec, nil
}

// GetDefaultTLSProfile returns the default TLS profile (Intermediate).
func GetDefaultTLSProfile() ocinfrav1.TLSProfileSpec {
	return ocinfrav1.TLSProfileSpec{
		Ciphers:       openshifttls.DefaultTLSCiphers,
		MinTLSVersion: openshifttls.DefaultMinTLSVersion,
	}
}

// BuildTLSConfig builds the TLS configuration from the given profile and options.
// It returns a Config containing the TLS options to apply to the webhook server.
func BuildTLSConfig(tlsProfileSpec ocinfrav1.TLSProfileSpec, enableHTTP2 bool, logger logr.Logger) *Config {
	// Build the TLS configuration function from the profile.
	tlsConfigFunc, unsupportedCiphers := openshifttls.NewTLSConfigFromProfile(tlsProfileSpec)
	if len(unsupportedCiphers) > 0 {
		logger.Info("Some ciphers from the TLS profile are not supported",
			"unsupportedCiphers", unsupportedCiphers)
	}

	// Build webhook TLS options.
	var webhookTLSOpts []func(*tls.Config)

	// Apply the TLS profile configuration first.
	webhookTLSOpts = append(webhookTLSOpts, tlsConfigFunc)

	// Disable HTTP/2 by default due to CVE-2023-44487 (HTTP/2 Rapid Reset Attack).
	// Applied AFTER the profile config to ensure it takes precedence over any
	// NextProtos settings from the profile.
	// Log the decision once here, not inside the closure which may be called multiple times.
	if !enableHTTP2 {
		logger.Info("Disabling HTTP/2 for webhook server")
		disableHTTP2 := func(c *tls.Config) {
			c.NextProtos = []string{"http/1.1"}
		}
		webhookTLSOpts = append(webhookTLSOpts, disableHTTP2)
	}

	return &Config{
		TLSProfileSpec:     tlsProfileSpec,
		TLSOpts:            webhookTLSOpts,
		UnsupportedCiphers: unsupportedCiphers,
	}
}

// ApplyTLSOptions applies the TLS options to a tls.Config.
// This is useful for testing the TLS configuration.
func ApplyTLSOptions(tlsOpts []func(*tls.Config), tlsConfig *tls.Config) {
	for _, opt := range tlsOpts {
		opt(tlsConfig)
	}
}

// GetTLSProfileType returns the TLS profile type name based on the profile spec.
// This is useful for logging and debugging.
func GetTLSProfileType(profile ocinfrav1.TLSProfileSpec) string {
	// Check against known profiles
	for profileType, knownProfile := range ocinfrav1.TLSProfiles {
		if knownProfile != nil && profilesMatch(profile, *knownProfile) {
			return string(profileType)
		}
	}
	return "Custom"
}

// profilesMatch compares two TLS profile specs for equality.
func profilesMatch(a, b ocinfrav1.TLSProfileSpec) bool {
	if a.MinTLSVersion != b.MinTLSVersion {
		return false
	}
	if len(a.Ciphers) != len(b.Ciphers) {
		return false
	}
	// Compare cipher lists - they should be in the same order for known profiles
	for i, cipher := range a.Ciphers {
		if cipher != b.Ciphers[i] {
			return false
		}
	}
	return true
}
