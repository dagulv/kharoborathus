package html

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/dagulv/kharoborathus/internal/lib/schedule"
	"github.com/dagulv/kharoborathus/internal/service/robot/adapter"
)

var subjects = []string{"räntan", "elpriset", "bostadsmarknaden", "exporten", "inflationen", "kronan", "arbetsmarknaden", "tillväxten", "budgeten", "valet", "skolan", "vården"}

type site struct {
	body    string
	updated string
}

func (s site) article(i int) string {
	var paras strings.Builder

	for p := range 8 {
		a, b, c := subjects[(i+p)%len(subjects)], subjects[(i+2*p+1)%len(subjects)], subjects[(i*3+p+2)%len(subjects)]
		fmt.Fprintf(&paras, "<p>Artikel %d, stycke %d, handlar om %s och hur %s påverkar %s under året som kommer.</p>\n", i, p, a, b, c)
	}

	return fmt.Sprintf(`<!doctype html>
<html><head><title>Rubrik %[1]d | Nyheter</title><meta property="og:title" content="Rubrik %[1]d"></head>
<body>
<header class="site-header"><a href="/">Nyheterna</a><nav class="menu"><a href="/a">Inrikes</a><a href="/b">Utrikes</a><a href="/c">Ekonomi</a></nav></header>
<div class="article-wrap">
  <h1 class="headline">Rubrik nummer %[1]d om %[2]s</h1>
  <div class="byline">Av <a href="/anna">Anna Svensson</a></div>
  <time datetime="2026-09-%02[3]dT08:00:00Z">%[3]d september</time>
  <div class="%[4]s" data-testid="article">
    <div class="updated">%[5]s</div>
    %[6]s
    <figure class="promo"><a href="/x">Läs också: Så sparar du pengar i vinter</a><img src="/p.jpg" alt=""></figure>
    <div class="newsletter-cta">Få morgonens viktigaste nyheter i din inkorg varje dag.</div>
  </div>
  <aside class="related"><h2>Mer att läsa</h2><ul>
    <li><a href="/r1">Relaterad artikel om ekonomin i landet och världen</a></li>
    <li><a href="/r2">Relaterad artikel om politiken i riksdagen denna vecka</a></li>
    <li><a href="/r3">Relaterad artikel om vädret i helgen och nästa vecka</a></li>
    <li><a href="/r4">Relaterad artikel om sporten och allsvenskans slutspurt</a></li>
  </ul></aside>
  <div class="comments"><h2>Kommentarer</h2><p>Kommentarsfältet är stängt för den här artikeln, läs våra regler för kommentarer.</p></div>
</div>
<div class="site-bottom"><p>Nyheterna AB, Storgatan 1, 111 22 Stockholm. Ansvarig utgivare Karin Karlsson. Om cookies och personuppgifter. Kontakta redaktionen dygnet runt via tipsa@nyheterna.se eller telefon.</p>
<p>Annonsera hos oss och nå hela Sverige varje dag, läs mer om våra annonsformat och priser på vår annonssida.</p></div>
<footer><a href="/om">Om oss</a></footer>
</body></html>`, i, subjects[i%len(subjects)], i%28+1, s.body, s.updated, paras.String())
}

func (s site) url(i int) string {
	return fmt.Sprintf("https://www.nyheterna.se/artikel/%d", i)
}

func str(s string) *string {
	return &s
}

func goodRule() Rule {
	return Rule{
		Include:     str("div.article-body"),
		Exclude:     []string{".promo", ".newsletter-cta", ".updated"},
		Title:       str("h1.headline"),
		Published:   str("time[datetime]"),
		Author:      str(".byline a"),
		ImagePolicy: ImageKeep,
		Reasoning:   "body is the shared container with unique text",
	}
}

type fakeLLM struct {
	mu      sync.Mutex
	answers []any
	calls   int
	prompts []string
	systems []string
}

func (f *fakeLLM) Schedule() schedule.Schedule { return schedule.Always }

func (f *fakeLLM) Prompt(context.Context, *slog.Logger, adapter.Request) (string, error) {
	return "", nil
}

func (f *fakeLLM) Unmarshal(_ context.Context, _ *slog.Logger, req adapter.Request, out any) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.prompts = append(f.prompts, req.Prompt)
	f.systems = append(f.systems, req.System)
	answer := f.answers[min(f.calls, len(f.answers)-1)]
	f.calls++

	b, err := json.Marshal(answer)
	if err != nil {
		return err
	}

	return json.Unmarshal(b, out)
}
