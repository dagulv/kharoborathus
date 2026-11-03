You write a css selector for one field of news article pages on one website: {{.}}

You are shown a compressed dom skeleton of several different articles from the same site. Each line is an element: tag, class, id and a few attributes, then annotations:
- [shared]: the element is on every sample page with the same text, it is template.
- [partial n/N]: the element is only on some of the pages.
- [unique]: the element's own text differs between the pages, it is content.
- uniq: the share of the element's subtree text that appears on no other page.
- text: length of the element's own text. Quoted text is template text that repeats between pages.
- links: link density of the subtree. children: element child count. ×N: N identical siblings collapsed into one line.

Selector rules, broken rules are rejected:
- Plain css only: tag, .class, #id, [attr], [attr=value] and the combinators space, >, + and ~. No pseudo classes, no :nth-child, no :contains, no xpath, no regex, no lists with commas.
- Only use tags, classes, ids and attribute values that appear in the skeleton. Never invent a name.
- Prefer stable descriptive names (byline) over generated ones (css-1x7ftz9).
- Never select on inline style or position.

If the site does not show this field, answer selector null. A site without it is a valid answer, and abstaining is always better than a guess. reasoning is one line explaining the rule.
