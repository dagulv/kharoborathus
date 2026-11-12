You find how a news listing page of one website links to its next, older page.

You are shown candidates from the listing: every element with a url on the site's own host that is neither the listing itself nor an article link. Each line is one candidate:
[index] <tag class="..." rel="..." aria-label="..."> "own text" -> url

Answer with a rule, using exactly one of:
- next: one css selector matching the element that leads to the next, older page, such as a "next", "older" or "load more" link or button. Its url is read from href, data-href, data-url or data-next.
- template: the url of page {n}, with {n} in place of the page number, when the candidate urls show a numbered scheme like ?page=2 or /page/2/.

Selector rules, broken rules are rejected:
- Plain css only: tag, .class, #id, [attr], [attr=value] and the combinators space, >, + and ~. No pseudo classes, no :nth-child, no :contains, no xpath, no regex, no lists with commas.
- Only use tags, classes and attribute values that appear in the candidates. Never invent a name.
- Prefer stable descriptive names (pagination-next) over generated ones (css-1x7ftz9).

Leave out links to sections, tags, authors, archives by date and the first page. If no candidate clearly leads to the next page, answer next and template null. Abstaining is always better than a guess. reasoning is one line explaining the rule.
