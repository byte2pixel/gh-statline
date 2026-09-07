package cmd

import (
	"strings"
	"testing"
)

// The release version reaches this package only through main passing an
// ldflags-injected string to Execute. Nothing else checks that it wins over
// the build-info fallback, so a broken injection would ship as a quietly
// wrong "statline dev".
func TestResolveVersionPrefersTheInjectedBuildVersion(t *testing.T) {
	saved := buildVersion
	t.Cleanup(func() { buildVersion = saved })

	buildVersion = "v0.4.0"
	if got := resolveVersion(); got != "v0.4.0" {
		t.Errorf("resolveVersion() = %q, want the injected version", got)
	}

	// Without an injected version the answer comes from build info, or "dev"
	// when there is none. Which one depends on how the binary was built, so
	// pin only that it never reports an empty version.
	buildVersion = ""
	if got := resolveVersion(); strings.TrimSpace(got) == "" {
		t.Error("resolveVersion() is empty with no injected version, want a fallback")
	}
}
