package ingest

import (
	"net/url"
	"regexp"
	"slices"
	"strings"
)

const maxLinksPerPost = 10

var markdownLink = regexp.MustCompile(`(!?)\[[^\[\]]*\]\(<?([^\s()<>]+)>?(?:\s+"[^"]*")?\)`)

var blockedHosts = []string{
	"facebook.com", "fb.com", "instagram.com", "threads.net", "twitter.com", "x.com", "t.co",
	"linkedin.com", "lnkd.in", "reddit.com", "pinterest.com", "bsky.app", "mastodon.social",
	"t.me", "whatsapp.com", "youtube.com", "youtu.be", "vimeo.com", "tiktok.com", "twitch.tv",
	"spotify.com", "soundcloud.com", "podcasts.apple.com", "apps.apple.com", "play.google.com",
	"drive.google.com", "docs.google.com", "dropbox.com", "wetransfer.com", "box.com",
	"onedrive.live.com", "1drv.ms", "issuu.com", "scribd.com", "slideshare.net",
	"flickr.com", "imgur.com", "giphy.com",
}

func outboundLinks(markdown, ownBaseURL string) []string {
	own := hostOf(ownBaseURL)

	var out []string

	for _, m := range markdownLink.FindAllStringSubmatch(markdown, -1) {
		if m[1] == "!" {
			continue
		}

		link, ok := normalizeLink(m[2])
		if !ok || slices.Contains(out, link) {
			continue
		}

		if host := hostOf(link); host == own || blocked(host) {
			continue
		}

		out = append(out, link)

		if len(out) == maxLinksPerPost {
			break
		}
	}

	return out
}

func normalizeLink(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", false
	}

	u.Fragment, u.RawFragment = "", ""

	q := u.Query()
	tracked := false

	for key := range q {
		if strings.HasPrefix(strings.ToLower(key), "utm_") {
			q.Del(key)
			tracked = true
		}
	}

	if tracked {
		u.RawQuery = q.Encode()
	}

	return u.String(), true
}

func hostOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}

	return strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
}

func blocked(host string) bool {
	return slices.ContainsFunc(blockedHosts, func(b string) bool {
		return host == b || strings.HasSuffix(host, "."+b)
	})
}
