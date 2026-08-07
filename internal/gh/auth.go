// Package gh provides GitHub authentication, the GraphQL client, and the
// query documents Statline runs against the GitHub API.
package gh

import (
	"errors"
	"os/exec"
	"strings"

	"github.com/cli/go-gh/v2/pkg/auth"
)

const host = "github.com"

// Token resolves a GitHub token: go-gh's resolution (GH_TOKEN/GITHUB_TOKEN
// env, then gh's config file), then a `gh auth token` subprocess — needed
// when gh stores the token in the OS keyring, which go-gh cannot read.
func Token() (string, error) {
	if t, _ := auth.TokenForHost(host); t != "" {
		return t, nil
	}
	if out, err := exec.Command("gh", "auth", "token", "--hostname", host).Output(); err == nil {
		if t := strings.TrimSpace(string(out)); t != "" {
			return t, nil
		}
	}
	return "", errors.New("no GitHub credentials found — run 'gh auth login' or set GITHUB_TOKEN")
}
