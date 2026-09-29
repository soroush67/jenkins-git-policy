// Package version holds the software version of git-policy.
//
// The software version (v0.1.0) is independent of the policy schema version
// (apiVersion: git-policy/v1): a new engine release can keep reading v1 policies.
package version

import "runtime"

// Set at build time via -ldflags "-X github.com/soroush67/git-policy/internal/version.Version=...".
var (
	Version   = "0.1.0-dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

// PolicyAPIVersion is the policy schema version this build understands.
const PolicyAPIVersion = "git-policy/v1"

// Platform returns the Go runtime version and target platform.
func Platform() string {
	return runtime.Version() + " " + runtime.GOOS + "/" + runtime.GOARCH
}
