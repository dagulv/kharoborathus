package fetcher

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/dagulv/kharoborathus/internal/lib/logs"
	"github.com/dagulv/kharoborathus/internal/lib/pace"
	"github.com/mmcdole/gofeed"
)

// Small pages, since a page carries full post content and a body is cut at maxPageSize.
const wordpressPageSize = 20

// The REST API announces itself with this relation in a Link header, on feeds too, and in the page head.
const wordpressRel = "https://api.w.org/"

type WordPress struct {
	Posts string `json:"posts"` // posts endpoint of the REST API
	Total int    `json:"total"`
	Lang  string `json:"-"` // of the page or feed it was found on, since the API gives none
}

// FindWordPress returns the REST API behind the page or feed at rawURL, or a zero WordPress when there is none.
func FindWordPress(ctx context.Context, client *http.Client, pacer *pace.Pacer, rawURL string) (WordPress, error) {
	if err := pacer.Wait(ctx, rawURL); err != nil {
		return WordPress{}, err
	}

	res, err := fetch(ctx, client, rawURL, anyAccept)
	if err != nil {
		return WordPress{}, fmt.Errorf("err fetch: %w", err)
	}

	root, lang := apiRoot(res)
	if root == "" {
		return WordPress{}, nil
	}

	posts, err := postsEndpoint(res.url, root)
	if err != nil {
		return WordPress{}, err
	}

	probeURL := withQuery(posts, "per_page", "1", "_fields", "id")

	if err := pacer.Wait(ctx, probeURL); err != nil {
		return WordPress{}, err
	}

	probe, err := fetch(ctx, client, probeURL, "application/json")
	if err != nil {
		return WordPress{}, fmt.Errorf("err probe posts: %w", err)
	}

	total, err := strconv.Atoi(probe.header.Get("X-WP-Total"))
	if err != nil {
		return WordPress{}, fmt.Errorf("err parse post total: %w", err)
	}

	return WordPress{Posts: posts.String(), Total: total, Lang: lang}, nil
}

func apiRoot(res response) (root, lang string) {
	root = linkHeader(res.header, wordpressRel)

	if !IsHTML(res.mediaType) {
		if f, err := gofeed.NewParser().Parse(bytes.NewReader(res.body)); err == nil {
			lang = f.Language
		}

		return root, lang
	}

	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(res.body))
	if err != nil {
		return root, ""
	}

	lang, _ = doc.Find("html").Attr("lang")

	if root == "" {
		root = strings.TrimSpace(doc.Find(`link[rel="`+wordpressRel+`"]`).AttrOr("href", ""))
	}

	return root, lang
}

// wp/v2/posts lists every post on the site, so only the site root or its main feed may stand for it.
// A section or category feed would import posts that are not its own.
func siteWide(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}

	path := strings.Trim(u.Path, "/")

	return path == "" || path == "feed"
}

func linkHeader(h http.Header, rel string) string {
	for _, v := range h.Values("Link") {
		for link := range strings.SplitSeq(v, ",") {
			target, params, ok := strings.Cut(link, ";")
			if !ok {
				continue
			}

			for param := range strings.SplitSeq(params, ";") {
				k, val, _ := strings.Cut(strings.TrimSpace(param), "=")

				if strings.EqualFold(k, "rel") && strings.Trim(val, `"`) == rel {
					return strings.Trim(strings.TrimSpace(target), "<>")
				}
			}
		}
	}

	return ""
}

// Without pretty permalinks the root is /?rest_route=/, which takes the route as a query value.
func postsEndpoint(baseURL, root string) (*url.URL, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("err parse base url: %w", err)
	}

	ref, err := url.Parse(root)
	if err != nil {
		return nil, fmt.Errorf("err parse api root: %w", err)
	}

	u := base.ResolveReference(ref)

	if q := u.Query(); q.Has("rest_route") {
		q.Set("rest_route", "/wp/v2/posts")
		u.RawQuery = q.Encode()

		return u, nil
	}

	return u.JoinPath("wp/v2/posts"), nil
}

func withQuery(u *url.URL, kv ...string) string {
	q := u.Query()

	for i := 0; i+1 < len(kv); i += 2 {
		q.Set(kv[i], kv[i+1])
	}

	out := *u
	out.RawQuery = q.Encode()

	return out.String()
}

type wordpressPost struct {
	Link    string `json:"link"`
	DateGMT string `json:"date_gmt"`
	Title   struct {
		Rendered string `json:"rendered"`
	} `json:"title"`
	Content struct {
		Rendered string `json:"rendered"`
	} `json:"content"`
	Embedded struct {
		Author []struct {
			Name string `json:"name"`
		} `json:"author"`
		Terms [][]struct {
			Name string `json:"name"`
		} `json:"wp:term"`
	} `json:"_embedded"`
}

func (r rss) wordpress(ctx context.Context, l *slog.Logger, url string) (<-chan rssDocument, bool, error) {
	return wordpressArchive(ctx, logs.Client(r.client, l, "wordpress"), r.pacer, l, url)
}

// wordpressArchive reads every post behind the page or feed at rawURL, newest first.
// A failed detection counts as no API, which covers a closed API answering 401 or 404.
func wordpressArchive(ctx context.Context, client *http.Client, pacer *pace.Pacer, l *slog.Logger, rawURL string) (<-chan rssDocument, bool, error) {
	if !siteWide(rawURL) {
		return nil, false, nil
	}

	wp, err := FindWordPress(ctx, client, pacer, rawURL)
	if err != nil {
		l.Warn("find wordpress", "url", rawURL, "err", err)
		return nil, false, nil
	}

	if wp.Posts == "" {
		return nil, false, nil
	}

	u, err := url.Parse(wp.Posts)
	if err != nil {
		return nil, true, fmt.Errorf("err parse posts url: %w", err)
	}

	first, pages, err := wordpressPage(ctx, client, pacer, u, 1, wp.Lang)
	if err != nil {
		return nil, true, fmt.Errorf("err fetch page 1: %w", err)
	}

	out := make(chan rssDocument)

	go func() {
		defer close(out)

		docs := first

		for page := 1; ; page++ {
			if page > 1 {
				if docs, _, err = wordpressPage(ctx, client, pacer, u, page, wp.Lang); err != nil {
					l.Error("fetch wordpress page", "url", wp.Posts, "page", page, "err", err)
					return
				}
			}

			for _, doc := range docs {
				select {
				case out <- doc:
				case <-ctx.Done():
					return
				}
			}

			if page >= pages {
				return
			}
		}
	}()

	return out, true, nil
}

func wordpressPage(ctx context.Context, client *http.Client, pacer *pace.Pacer, posts *url.URL, page int, lang string) ([]rssDocument, int, error) {
	pageURL := withQuery(posts,
		"per_page", strconv.Itoa(wordpressPageSize),
		"page", strconv.Itoa(page),
		"_embed", "author,wp:term",
		"_fields", "link,date_gmt,title,content,_links,_embedded",
	)

	if err := pacer.Wait(ctx, pageURL); err != nil {
		return nil, 0, err
	}

	res, err := fetch(ctx, client, pageURL, "application/json")
	if err != nil {
		return nil, 0, err
	}

	var items []wordpressPost

	if err := json.Unmarshal(res.body, &items); err != nil {
		return nil, 0, fmt.Errorf("err decode posts: %w", err)
	}

	pages, _ := strconv.Atoi(res.header.Get("X-WP-TotalPages"))

	docs := make([]rssDocument, 0, len(items))

	for _, item := range items {
		doc := item.document()
		doc.Lang = lang
		docs = append(docs, doc)
	}

	return docs, pages, nil
}

// The API escapes titles and term names as html, where gofeed hands them over plain.
func (p wordpressPost) document() rssDocument {
	doc := rssDocument{
		URL:     p.Link,
		Title:   html.UnescapeString(p.Title.Rendered),
		Content: p.Content.Rendered,
	}

	if t, err := time.Parse("2006-01-02T15:04:05", p.DateGMT); err == nil {
		doc.PostedAt = t
	}

	for _, author := range p.Embedded.Author {
		if author.Name != "" {
			doc.Authors = append(doc.Authors, html.UnescapeString(author.Name))
		}
	}

	for _, taxonomy := range p.Embedded.Terms {
		for _, term := range taxonomy {
			doc.Tags = append(doc.Tags, html.UnescapeString(term.Name))
		}
	}

	return doc
}
