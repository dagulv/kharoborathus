package feed

import (
	"math/bits"
	"strings"
	"testing"
)

const article = `Volvo Cars tecknar avtal om att förvärva det svenska batteribolaget Northvolts fabrik i Göteborg. Affären omfattar anläggningen i Torslanda samt omkring 300 anställda som går över till Volvo Cars. Köpeskillingen uppgår till 1,2 miljarder kronor och betalas kontant vid tillträdet som väntas ske under det fjärde kvartalet. Enligt bolaget ska fabriken användas för att tillverka battericeller till kommande elbilsmodeller. Affären är villkorad av godkännande från konkurrensmyndigheter i EU. Volvo Cars vd säger i ett pressmeddelande att förvärvet stärker bolagets kontroll över leveranskedjan och minskar beroendet av asiatiska leverantörer. Northvolt har sedan konkursen i våras sökt köpare till sina olika verksamheter. Aktien steg med tre procent i tidig handel på Stockholmsbörsen efter beskedet. Analytiker bedömer att affären är positiv för bolaget på lång sikt men att investeringsbehovet de närmaste åren blir betydande. Fabriken har en kapacitet på flera gigawattimmar per år.`

const otherArticle = `Ericsson redovisar ett rörelseresultat för tredje kvartalet som överträffar analytikernas förväntningar. Nettoomsättningen minskade med fyra procent jämfört med samma period i fjol, främst på grund av svag efterfrågan i Nordamerika. Bruttomarginalen förbättrades dock till 47 procent tack vare kostnadsbesparingar. Bolaget upprepar sin prognos för helåret och räknar med en återhämtning under nästa år. Aktien handlades upp sju procent under förmiddagen.`

func distance(t *testing.T, a, b string) int {
	t.Helper()

	ha, ok := simhash(a)
	if !ok {
		t.Fatalf("no simhash for %q", a)
	}

	hb, ok := simhash(b)
	if !ok {
		t.Fatalf("no simhash for %q", b)
	}

	return bits.OnesCount64(ha ^ hb)
}

func TestSimhashSameText(t *testing.T) {
	cases := map[string]string{
		"identical": article,
		"case":      strings.ToUpper(article),
		"spacing":   strings.ReplaceAll(article, " ", "\n\n  "),
		"link":      strings.Replace(article, "Northvolts", "[Northvolts](https://a.example/x?y=1)", 1),
		"image":     article + "\n\n![](https://a.example/img.jpg)",
	}

	for name, text := range cases {
		if d := distance(t, article, text); d != 0 {
			t.Errorf("%s: distance %d", name, d)
		}
	}
}

func TestSimhashSmallEdit(t *testing.T) {
	cases := map[string]string{
		"one word": strings.Replace(article, "1,2 miljarder", "1,3 miljarder", 1),
		"byline":   "TT\n\n" + article + "\n\nPublicerad 08:12",
	}

	for name, text := range cases {
		if d := distance(t, article, text); d > 3 {
			t.Errorf("%s: distance %d", name, d)
		}
	}
}

func TestSimhashOtherText(t *testing.T) {
	if d := distance(t, article, otherArticle); d <= 10 {
		t.Errorf("distance %d", d)
	}
}

func TestSimhashShortText(t *testing.T) {
	if d := distance(t, "Två ord", "två, ord!"); d != 0 {
		t.Errorf("distance %d", d)
	}

	if d := distance(t, "Två ord", "Tre ord"); d == 0 {
		t.Error("different short texts share a simhash")
	}
}

func TestSimhashNoWords(t *testing.T) {
	for _, text := range []string{"", "  \n ", "-- ! ?", "![](https://a.example/img.jpg)"} {
		if _, ok := simhash(text); ok {
			t.Errorf("simhash for %q", text)
		}
	}
}
