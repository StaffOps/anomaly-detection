// Package version exposes the controller/worker build version.
//
// Update this constant on every release per `version-management.md` steering.
// Versions follow SemVer 2.0: MAJOR.MINOR.PATCH.
package version

// Version is the semantic version of the controller and worker binaries.
// Both ship together (same module), so they share a single version.
const Version = "0.10.0"
