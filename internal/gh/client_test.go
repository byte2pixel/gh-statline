package gh

import (
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
)

func TestClassify(t *testing.T) {
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	httpErr := func(code int, msg string, hdr ...string) error {
		h := http.Header{}
		for i := 0; i+1 < len(hdr); i += 2 {
			h.Set(hdr[i], hdr[i+1])
		}
		return &api.HTTPError{StatusCode: code, Message: msg, Headers: h}
	}
	gqlErr := func(types ...string) error {
		e := &api.GraphQLError{}
		for _, ty := range types {
			e.Errors = append(e.Errors, api.GraphQLErrorItem{Type: ty, Message: ty})
		}
		return e
	}
	reset := func(d time.Duration) string { return strconv.FormatInt(now.Add(d).Unix(), 10) }
	limited := func(wait time.Duration) Retryability {
		return Retryability{Retry: true, RateLimited: true, Wait: wait}
	}

	cases := []struct {
		name string
		err  error
		want Retryability
	}{
		{"server error 500", httpErr(500, "x"), Retryability{Retry: true}},
		{"bad gateway 502", httpErr(502, "x"), Retryability{Retry: true}},
		{"not found 404", httpErr(404, "x"), Retryability{}},
		{"unauthorized 401", httpErr(401, "x"), Retryability{}},

		// 429 is always a rate limit: Retry-After wins, else the floor.
		{"429 with Retry-After", httpErr(429, "x", "Retry-After", "90"), limited(90 * time.Second)},
		{"429 without headers", httpErr(429, "x"), limited(secondaryLimitWait)},
		{"429 Retry-After unreadable", httpErr(429, "x", "Retry-After", "soon"), limited(secondaryLimitWait)},

		// 403 retries only on a rate-limit signal; the rest is permanent.
		{"403 primary quota spent", httpErr(403, "API rate limit exceeded for user",
			"X-Ratelimit-Remaining", "0", "X-Ratelimit-Reset", reset(25*time.Minute)), limited(25 * time.Minute)},
		{"403 with Retry-After", httpErr(403, "You have exceeded a secondary rate limit",
			"Retry-After", "120"), limited(120 * time.Second)},
		{"403 secondary limit by message only", httpErr(403,
			"You have exceeded a secondary rate limit. Please wait a few minutes before you try again."), limited(secondaryLimitWait)},
		{"403 quota spent, reset unreadable", httpErr(403, "x", "X-Ratelimit-Remaining", "0"), limited(secondaryLimitWait)},
		{"403 SAML enforcement", httpErr(403, "Resource protected by organization SAML enforcement.",
			"X-Ratelimit-Remaining", "4999"), Retryability{}},
		{"403 missing scope", httpErr(403, "Resource not accessible by personal access token"), Retryability{}},
		{"403 no headers at all", &api.HTTPError{StatusCode: 403, Message: "Forbidden"}, Retryability{}},

		// Server-mandated waits are honoured but bounded.
		{"Retry-After capped", httpErr(429, "x", "Retry-After", "3600"), limited(maxRetryAfter)},
		{"reset capped at the window", httpErr(403, "x",
			"X-Ratelimit-Remaining", "0", "X-Ratelimit-Reset", reset(3*time.Hour)), limited(maxResetWait)},
		{"reset already passed", httpErr(403, "x",
			"X-Ratelimit-Remaining", "0", "X-Ratelimit-Reset", reset(-time.Minute)), limited(0)},
		{"reset ignored when quota remains", httpErr(429, "x",
			"X-Ratelimit-Remaining", "4000", "X-Ratelimit-Reset", reset(50*time.Minute)), limited(secondaryLimitWait)},

		// GraphQL: every item must be transient.
		{"graphql empty", &api.GraphQLError{}, Retryability{}},
		{"graphql INTERNAL", gqlErr("INTERNAL"), Retryability{Retry: true}},
		{"graphql SERVICE_UNAVAILABLE", gqlErr("SERVICE_UNAVAILABLE"), Retryability{Retry: true}},
		{"graphql two transient items", gqlErr("INTERNAL", "TIMEOUT"), Retryability{Retry: true}},
		{"graphql NOT_FOUND", gqlErr("NOT_FOUND"), Retryability{}},
		{"graphql FORBIDDEN", gqlErr("FORBIDDEN"), Retryability{}},
		{"graphql untyped", gqlErr(""), Retryability{}},
		{"graphql transient plus semantic", gqlErr("INTERNAL", "NOT_FOUND"), Retryability{}},
		{"graphql RATE_LIMITED", gqlErr("RATE_LIMITED"), limited(secondaryLimitWait)},

		{"transport error", errors.New("connection reset"), Retryability{Retry: true}},
		{"wrapped http error", errors.Join(errors.New("ctx"), httpErr(503, "x")), Retryability{Retry: true}},
		{"wrapped permanent 403", fmt.Errorf("fetching: %w", httpErr(403, "SAML")), Retryability{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Classify(c.err, now); got != c.want {
				t.Errorf("Classify(%v) = %+v, want %+v", c.err, got, c.want)
			}
		})
	}
}

// gh exports GH_PATH when it runs an extension. Preferring it avoids a PATH
// search entirely, so the token cannot be handed to a different gh.
func TestGhPathPrefersEnv(t *testing.T) {
	t.Setenv("GH_PATH", filepath.Join("some", "dir", "gh"))
	got, err := ghPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join("some", "dir", "gh"); got != want {
		t.Errorf("ghPath = %q, want %q", got, want)
	}
}

func TestStderrOfExitError(t *testing.T) {
	if got := stderrOf(errors.New("plain")); got != "" {
		t.Errorf("non-exit error returned %q, want empty", got)
	}
	exit := &exec.ExitError{Stderr: []byte("  not logged in\n")}
	if got := stderrOf(exit); got != "not logged in" {
		t.Errorf("stderrOf = %q, want \"not logged in\"", got)
	}
}

func TestActorSafeLoginAndIsBot(t *testing.T) {
	var ghost *Actor
	if ghost.SafeLogin() != "ghost" {
		t.Errorf("nil actor SafeLogin = %q, want ghost", ghost.SafeLogin())
	}
	if ghost.IsBot() {
		t.Error("nil actor reported as bot")
	}
	empty := &Actor{}
	if empty.SafeLogin() != "ghost" {
		t.Errorf("empty-login actor SafeLogin = %q, want ghost", empty.SafeLogin())
	}
	bot := &Actor{Login: "dependabot", TypeName: "Bot"}
	if !bot.IsBot() || bot.SafeLogin() != "dependabot" {
		t.Errorf("bot actor: IsBot=%v SafeLogin=%q", bot.IsBot(), bot.SafeLogin())
	}
	user := &Actor{Login: "alice", TypeName: "User"}
	if user.IsBot() {
		t.Error("user actor reported as bot")
	}
	// Every login-less actor is stored as "ghost", and users.is_bot is
	// last-write-wins per login: flagging one would drop every deleted
	// account's reviews and comments from the numbers at once.
	anon := &Actor{TypeName: "Bot"}
	if anon.IsBot() {
		t.Error("login-less bot actor must not flag the shared ghost row")
	}
}
