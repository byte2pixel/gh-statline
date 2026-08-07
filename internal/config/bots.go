package config

import (
	"regexp"
	"strings"
	"sync"
)

// BotMatcher reports whether a login matches the exclude_bots globs.
// Globs support * (any run) and ? (any single char); all other characters
// are literal — notably [ and ], since patterns like "*[bot]" must match
// the literal brackets GitHub puts in bot logins (path.Match would treat
// them as a character class).
type BotMatcher struct {
	res []*regexp.Regexp

	mu    sync.Mutex
	cache map[string]bool
}

func NewBotMatcher(globs []string) *BotMatcher {
	m := &BotMatcher{cache: map[string]bool{}}
	for _, g := range globs {
		m.res = append(m.res, globToRegexp(g))
	}
	return m
}

func (m *BotMatcher) IsBot(login string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if v, ok := m.cache[login]; ok {
		return v
	}
	v := false
	for _, re := range m.res {
		if re.MatchString(login) {
			v = true
			break
		}
	}
	m.cache[login] = v
	return v
}

func globToRegexp(glob string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("(?i)^")
	for _, r := range glob {
		switch r {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String())
}
