// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package fingerprint computes deterministic identity fingerprints for components.
// A fingerprint captures all resolved build inputs so that changes to any input
// (config fields, spec content, overlay files, distro context, upstream refs, or
// ManualBump) produce a different fingerprint.
//
// The primary entry point is [ComputeIdentity], which takes a resolved
// [projectconfig.ComponentConfig] and additional context, and returns a
// [ComponentIdentity] holding the atomic "v<N>:sha256:..." content token.
package fingerprint
