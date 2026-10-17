package ingest

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	userAgent     = "kharoborathus/1.0"
	robotsAgent   = "kharoborathus"
	robotsTTL     = 24 * time.Hour
	maxRobotsSize = 512 << 10
)

type robotsRule struct {
	allow   bool
	pattern *regexp.Regexp
	length  int
}

type robotsRules []robotsRule

type robotsEntry struct {
	rules     robotsRules
	fetchedAt time.Time
}

type Robots struct {
	client *http.Client
	pacer  *pacer
	cache  map[string]robotsEntry
}

func NewRobots(client *http.Client, p *pacer) *Robots {
	return &Robots{client: client, pacer: p, cache: make(map[string]robotsEntry)}
}

func (r *Robots) Allowed(ctx context.Context, rawURL string) (bool, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false, fmt.Errorf("err parse url: %w", err)
	}

	rules, err := r.rules(ctx, u)
	if err != nil {
		return false, err
	}

	return rules.allowed(u.RequestURI()), nil
}

func (r *Robots) rules(ctx context.Context, u *url.URL) (robotsRules, error) {
	origin := strings.ToLower(u.Scheme + "://" + u.Host)

	if e, ok := r.cache[origin]; ok && time.Since(e.fetchedAt) < robotsTTL {
		return e.rules, nil
	}

	if err := r.pacer.Wait(ctx, hostOf(origin)); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+"/robots.txt", nil)
	if err != nil {
		return nil, fmt.Errorf("err new robots request: %w", err)
	}

	req.Header.Set("User-Agent", userAgent)

	res, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("err fetch robots.txt: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode >= http.StatusInternalServerError || res.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("err fetch robots.txt: status %s", res.Status)
	}

	// Any other non-200 status means there is no robots.txt, which allows everything.
	var rules robotsRules

	if res.StatusCode == http.StatusOK {
		body, err := io.ReadAll(io.LimitReader(res.Body, maxRobotsSize))
		if err != nil {
			return nil, fmt.Errorf("err read robots.txt: %w", err)
		}

		rules = parseRobots(body, robotsAgent)
	}

	r.cache[origin] = robotsEntry{rules: rules, fetchedAt: time.Now()}

	return rules, nil
}

func parseRobots(body []byte, agent string) robotsRules {
	var specific, wildcard robotsRules
	var agents []string
	var hasSpecific, inRules bool

	for line := range strings.Lines(string(body)) {
		line, _, _ = strings.Cut(line, "#")

		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}

		key, value = strings.ToLower(strings.TrimSpace(key)), strings.TrimSpace(value)

		switch key {
		case "user-agent":
			if inRules {
				agents, inRules = nil, false
			}

			value = strings.ToLower(value)
			agents = append(agents, value)
			hasSpecific = hasSpecific || value == agent
		case "allow", "disallow":
			inRules = true

			if value == "" {
				continue
			}

			rule := robotsRule{allow: key == "allow", pattern: robotsPattern(value), length: len(value)}

			for _, a := range agents {
				switch a {
				case agent:
					specific = append(specific, rule)
				case "*":
					wildcard = append(wildcard, rule)
				}
			}
		}
	}

	if hasSpecific {
		return specific
	}

	return wildcard
}

func robotsPattern(path string) *regexp.Regexp {
	expr := "^" + strings.ReplaceAll(regexp.QuoteMeta(strings.TrimSuffix(path, "$")), `\*`, ".*")

	if strings.HasSuffix(path, "$") {
		expr += "$"
	}

	return regexp.MustCompile(expr)
}

// The longest matching rule wins, and allow wins a tie.
func (r robotsRules) allowed(path string) bool {
	best, allow := -1, true

	for _, rule := range r {
		if rule.length < best || !rule.pattern.MatchString(path) {
			continue
		}

		if rule.length > best || rule.allow {
			best, allow = rule.length, rule.allow
		}
	}

	return allow
}
