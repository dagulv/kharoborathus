# Categories

## Intent

Tags are flat. Nothing groups `annual_report`, `quarterly_report` and `dividend` under finance. You want categories to browse the feed by and, later, to scope analyses to a field. A category is a group of tags, and a tag can be in several categories.

Categories also cut Jev's work. Today Jev asks one question per active tag on every post. With categories it first asks about the categories, then only about the tags in the categories that matched.

Categories are a fixed list. This design generates the first list and its tags as a seed. After that the system only adds tags, and only you add categories, in the admin.

This comes before entities. Entities will later hang off the same structure.

## Goals

- A seeded set of categories and tags, with Swedish and English terms, in a numbered migration.
- Many-to-many mapping between categories and tags.
- Two-stage Jev in enrich: categories first, then the tags of matched categories plus every tag without an enabled category.
- New proposed tags get categories from the same DeepSeek call that proposes them.
- The public feed filters by category.
- The admin lists, creates and switches categories, and edits a tag's categories.

## Non-goals

- Analyses. Categories only make them possible later.
- Entities. They are the next design.
- The system proposing categories, or any category lifecycle.
- Deleting or renaming categories. Disabling is enough.
- Category browsing in the public web app. It is rebuilt later in a separate project. This design gives it the API.
- Tag promotion. It is still missing from the reworked code.
- Fixing that topic tags are `quality` and can therefore be discovery actions. That is true of proposals today too.

## Anti-goals

- A tag must never become invisible to Jev because of categories. A tag whose categories are all disabled, or that has none, is always asked.
- The system must never create or enable a category.
- The seed must never overwrite your `enabled` switch on an existing tag.
- A merged tag must never lose its categories.

## Approaches

How the seed ships:

- **A numbered SQL migration.** Picked. Runs once per database, and in prod CI runs it before deploy. Tags you later disable or merge stay that way. Fits the rule that every change is a new numbered file.
- **A Go seed at every startup.** Rejected. It would re-create tags you deleted or merged away.
- **A `cmd seed` subcommand.** Rejected. One more manual step per environment, easy to forget in prod.

How categories come about: you create them by hand, and the seed gives the first set. The system proposing categories was considered and dropped, since it needs a promotion lifecycle for a list of under twenty.

## Design

### Schema

New file `internal/migrations/002_categories.sql`. `001_init.sql` is shipped and stays untouched.

```sql
CREATE TABLE IF NOT EXISTS categories (
  key TEXT PRIMARY KEY,
  enabled BOOLEAN NOT NULL DEFAULT true,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One label per language. Jev questions and the UI use it.
CREATE TABLE IF NOT EXISTS category_terms (
  key TEXT NOT NULL REFERENCES categories (key) ON DELETE CASCADE,
  lang TEXT NOT NULL,
  term TEXT NOT NULL,
  PRIMARY KEY (key, lang)
);

CREATE TABLE IF NOT EXISTS category_tags (
  category TEXT NOT NULL REFERENCES categories (key) ON DELETE CASCADE,
  tag TEXT NOT NULL REFERENCES tags (key) ON DELETE CASCADE,
  PRIMARY KEY (category, tag)
);

CREATE INDEX IF NOT EXISTS category_tags_tag_idx ON category_tags (tag);
```

`posts` gets no column. A post is in a category when one of its public tags, meaning enabled and active, is mapped to that enabled category. Browsing and analyses then agree with the tags shown on the post.

Tern runs files through `text/template`, so the SQL must not contain `{{`.

### Seed

In the same migration, after the tables.

- Categories are inserted with their `sv` and `en` terms.
- Tags are inserted as `status = 'active'`, `type = 'quality'`, with one `sv` and one `en` term each.
- A seed key that already exists as a tag becomes `active`. Its `enabled` is kept.
- A seed key that is an alias in `tag_aliases` is not inserted as a tag. Its terms and categories go to the alias target, and the target becomes `active` with its `enabled` kept. This keeps the rule that an alias is never also a key.
- Terms use `ON CONFLICT DO NOTHING`.
- `category_tags` rows resolve tag keys through `tag_aliases` first.

The seed is not reconciled with other keys already in prod for the same concept. You merge such duplicates in the admin after deploy.

The running API still has the old code while the migration runs. The new tables are unused by it, and the new active tags are classified by its one-stage Jev until the deploy finishes. Both are safe.

#### Categories

| key | sv | en |
|---|---|---|
| finance | finans | finance |
| markets | aktiemarknad | stock market |
| deals | företagsaffär | corporate transaction |
| governance | bolagsstyrning | corporate governance |
| workforce | arbetsmarknad | labour market |
| macro | makroekonomi | macroeconomics |
| policy | politik och regelverk | policy and regulation |
| banking | bank och försäkring | banking and insurance |
| energy | energi | energy |
| automotive | fordon | automotive |
| technology | teknik | technology |
| industry | industri | industry |
| real_estate | fastighet | real estate |
| healthcare | hälsa och life science | healthcare and life science |
| consumer | konsument | consumer |
| defense | försvar | defense |
| transport | transport och logistik | transport and logistics |
| sustainability | hållbarhet | sustainability |

#### Tags

Swedish terms are in base form: singular, indefinite, lowercase, as the enrich prompt asks.

| key | sv | en | categories |
|---|---|---|---|
| annual_report | årsredovisning | annual report | finance |
| quarterly_report | kvartalsrapport | quarterly report | finance |
| earnings | resultat | earnings | finance |
| guidance | prognos | guidance | finance |
| profit_warning | vinstvarning | profit warning | finance, markets |
| dividend | utdelning | dividend | finance, markets |
| share_buyback | återköp av aktier | share buyback | finance, markets |
| new_share_issue | nyemission | share issue | finance, markets |
| rights_issue | företrädesemission | rights issue | finance, markets |
| bond_issue | obligationsemission | bond issue | finance, markets |
| refinancing | refinansiering | refinancing | finance |
| funding_round | finansieringsrunda | funding round | finance |
| venture_capital | riskkapital | venture capital | finance |
| private_equity | private equity | private equity | finance, deals |
| bankruptcy | konkurs | bankruptcy | finance |
| restructuring | rekonstruktion | restructuring | finance |
| credit_rating | kreditbetyg | credit rating | finance, banking |
| write_down | nedskrivning | write-down | finance |
| cost_cutting | besparing | cost cutting | finance, workforce |
| ipo | börsnotering | ipo | markets, finance |
| delisting | avnotering | delisting | markets |
| short_selling | blankning | short selling | markets |
| insider_trading | insynshandel | insider trading | markets, governance |
| stock_index | börsindex | stock index | markets |
| analyst_rating | analytikerrekommendation | analyst rating | markets |
| ownership_change | ägarförändring | ownership change | markets, deals |
| acquisition | förvärv | acquisition | deals |
| merger | fusion | merger | deals |
| divestment | avyttring | divestment | deals |
| spin_off | avknoppning | spin-off | deals, markets |
| public_takeover_bid | offentligt uppköpserbjudande | public takeover bid | deals, markets |
| joint_venture | samriskbolag | joint venture | deals |
| partnership | samarbete | partnership | deals |
| contract | avtal | contract | deals |
| order | order | order | deals, industry |
| ceo_change | vd-byte | ceo change | governance |
| board_change | styrelseförändring | board change | governance |
| annual_general_meeting | årsstämma | annual general meeting | governance |
| executive_compensation | ersättning till ledande befattningshavare | executive compensation | governance |
| nomination_committee | valberedning | nomination committee | governance |
| incentive_program | incitamentsprogram | incentive program | governance |
| layoffs | varsel | layoffs | workforce |
| hiring | rekrytering | hiring | workforce |
| strike | strejk | strike | workforce |
| collective_agreement | kollektivavtal | collective agreement | workforce |
| unemployment | arbetslöshet | unemployment | workforce, macro |
| labour_shortage | kompetensbrist | labour shortage | workforce |
| interest_rate | ränta | interest rate | macro, banking |
| inflation | inflation | inflation | macro |
| central_bank | centralbank | central bank | macro, banking |
| gdp | bnp | gdp | macro |
| recession | lågkonjunktur | recession | macro |
| exchange_rate | växelkurs | exchange rate | macro, markets |
| housing_market | bostadsmarknad | housing market | macro, real_estate |
| government_budget | statsbudget | government budget | macro, policy |
| foreign_trade | utrikeshandel | foreign trade | macro, policy |
| household_consumption | hushållskonsumtion | household consumption | macro, consumer |
| regulation | reglering | regulation | policy |
| tax | skatt | tax | policy |
| sanctions | sanktion | sanction | policy |
| tariffs | tull | tariff | policy, macro |
| state_aid | statsstöd | state aid | policy |
| competition_law | konkurrensrätt | competition law | policy, deals |
| lawsuit | stämning | lawsuit | policy |
| fine | böter | fine | policy |
| public_procurement | offentlig upphandling | public procurement | policy, deals |
| banking | bank | banking | banking |
| insurance | försäkring | insurance | banking |
| fintech | fintech | fintech | banking, technology |
| lending | utlåning | lending | banking |
| payments | betalning | payments | banking, technology |
| money_laundering | penningtvätt | money laundering | banking, policy |
| cryptocurrency | kryptovaluta | cryptocurrency | banking, technology |
| asset_management | kapitalförvaltning | asset management | banking, markets |
| nuclear_power | kärnkraft | nuclear power | energy |
| wind_power | vindkraft | wind power | energy, sustainability |
| solar_power | solenergi | solar power | energy, sustainability |
| hydropower | vattenkraft | hydropower | energy |
| electricity_price | elpris | electricity price | energy, macro |
| power_grid | elnät | power grid | energy |
| oil | olja | oil | energy |
| natural_gas | naturgas | natural gas | energy |
| hydrogen | vätgas | hydrogen | energy, industry, sustainability |
| energy_storage | energilagring | energy storage | energy |
| batteries | batteri | battery | energy, automotive, industry |
| cars | bil | car | automotive |
| electric_vehicles | elbil | electric vehicle | automotive, sustainability |
| trucks | lastbil | truck | automotive, transport |
| autonomous_driving | självkörande fordon | autonomous driving | automotive, technology |
| charging_infrastructure | laddinfrastruktur | charging infrastructure | automotive, energy |
| car_sales | bilförsäljning | car sales | automotive, consumer |
| artificial_intelligence | artificiell intelligens | artificial intelligence | technology |
| semiconductors | halvledare | semiconductor | technology, industry |
| software | mjukvara | software | technology |
| cybersecurity | cybersäkerhet | cybersecurity | technology, defense |
| cloud_computing | molntjänst | cloud computing | technology |
| telecom | telekom | telecom | technology |
| data_center | datacenter | data center | technology, energy, real_estate |
| quantum_computing | kvantdator | quantum computing | technology |
| video_games | dataspel | video games | technology, consumer |
| product_launch | produktlansering | product launch | technology, consumer |
| manufacturing | tillverkning | manufacturing | industry |
| new_factory | ny fabrik | new factory | industry |
| steel | stål | steel | industry |
| mining | gruvdrift | mining | industry |
| forest_industry | skogsindustri | forest industry | industry, sustainability |
| chemical_industry | kemiindustri | chemical industry | industry |
| construction | byggbransch | construction | industry, real_estate |
| supply_chain | leveranskedja | supply chain | industry, transport |
| raw_materials | råvara | raw material | industry, markets |
| infrastructure | infrastruktur | infrastructure | industry, transport |
| commercial_real_estate | kommersiell fastighet | commercial real estate | real_estate |
| residential_property | bostad | residential property | real_estate |
| property_transaction | fastighetsaffär | property transaction | real_estate, deals |
| rent | hyra | rent | real_estate |
| pharmaceuticals | läkemedel | pharmaceutical | healthcare |
| medtech | medicinteknik | medical technology | healthcare, technology |
| biotech | bioteknik | biotechnology | healthcare |
| clinical_trial | klinisk studie | clinical trial | healthcare |
| drug_approval | läkemedelsgodkännande | drug approval | healthcare, policy |
| healthcare_services | vård | healthcare services | healthcare |
| retail | detaljhandel | retail | consumer |
| e_commerce | e-handel | e-commerce | consumer, technology |
| consumer_goods | konsumentvara | consumer goods | consumer |
| food_industry | livsmedelsindustri | food industry | consumer, industry |
| tourism | turism | tourism | consumer, transport |
| defense_industry | försvarsindustri | defense industry | defense, industry |
| defense_spending | försvarsutgift | defense spending | defense, policy |
| shipping | sjöfart | shipping | transport |
| aviation | flyg | aviation | transport |
| rail | järnväg | rail | transport |
| logistics | logistik | logistics | transport |
| climate | klimat | climate | sustainability |
| emissions | utsläpp | emissions | sustainability, policy |
| carbon_capture | koldioxidinfångning | carbon capture | sustainability, industry |
| esg | esg | esg | sustainability, finance |
| circular_economy | cirkulär ekonomi | circular economy | sustainability |

18 categories and 137 tags. Every tag is in at least one category.

### Two-stage Jev in enrich

Replaces the single `jevTags` pass in step 3 of `Enrich`. Same state: title and markdown cut at 60,000 runes.

1. **Categories.** One request with a question per enabled category, in the existing form: `Is this news article about <key> (<term in the post's language>)?`. A post in another language, or a category with no term in it, gets the question without the term. A score of at least 0.5 matches the category.
2. **Tags.** The candidate tags are every enabled, active tag that is mapped to a matched category, plus every enabled, active tag with no enabled category. Asked in batches of 50 as today. A score of at least 0.8 adds the key.

If no category is enabled, stage 1 is skipped and every active tag is a candidate. That is today's behaviour.

The examples in the enrich system prompt's tags section change to seed keys, such as `quarterly_report` instead of `earnings_report`, so DeepSeek reuses them.

The tag cache in `cache.go` also holds the enabled categories with their term per language and the tag to category map, under the same one-minute TTL.

Jev still runs before DeepSeek. Proposed tags are still never sent to Jev.

### Categories for proposed tags

The enrich DeepSeek call already proposes tags. Each proposal gains `categories`:

```json
{"key": "solid_state_battery", "term": "solid state-batteri", "categories": ["energy", "automotive"]}
```

- The user prompt lists the enabled category keys. The system prompt says: pick one or more of the listed categories for each tag, or none when nothing fits. It stays static, so DeepSeek can still cache it.
- Unknown or disabled keys are dropped.
- `SaveProposals` writes `category_tags` rows only for a tag its own insert just created. The first post that proposes a tag sets its categories. Later proposals never touch them, so clearing a tag's categories in the admin sticks. Active tags and your edits are never touched.
- Untrusted posts still propose nothing.

A proposed tag keeps its categories when it later becomes active.

### Tag merge

Merging tag `from` into `to` copies `from`'s `category_tags` rows to `to` with `ON CONFLICT DO NOTHING` before `from` is deleted. Today that is `mergeTag` in `frontend/src/routes/admin/admin.remote.ts`. After the admin SPA ships it is `POST /admin/tags/merge` in Go. The change goes wherever merge lives when this is built.

### Public API

- `GET /posts?category=<key>`: a post must be in every given category, the same AND rule as `tag`. Filter: `v.tags && ARRAY(SELECT ct.tag FROM category_tags ct JOIN categories c ON c.key = ct.category WHERE c.key = $n AND c.enabled)`. An unknown or disabled category gives an empty list.
- `GET /categories`: enabled categories, each with its terms and its public tag keys, ordered by key.

### Admin

Built on the admin SPA from `2026-11-14-admin-spa-design.md`, so it lands after that plan. Same JSON and validation style.

| Method and path | Body | Answer |
|---|---|---|
| `GET /admin/categories` | none | All categories with terms, `enabled`, tag count and post count |
| `POST /admin/categories` | `{key, sv, en}` | 201, 400 on validation, 409 when the key exists |
| `PATCH /admin/categories/{key}` | `{enabled}` | 204 or 404 |
| `PUT /admin/tags/{key}/categories` | `{categories: [key]}` | 204, 400 for an unknown category, 404 for an unknown tag |

- `GET /admin/tags` adds each tag's categories.
- `GET /admin/posts` accepts `category`. Unlike the public route it matches on all of a post's tags, `p.tags`, like the admin tag filter, so posts carried only by proposed or disabled tags show up for review. The post count in `GET /admin/categories` uses the same rule.
- CORS preflight allows `PUT`.
- A Categories page lists categories, creates one and switches it. A category links to its posts.
- The Tags page shows each tag's categories, filters by category, and edits them with a multi-select.
- Category keys follow the tag key rule: 1 to 40 characters, normalized with `TagKey`. Both terms are required, at most 100 characters.

## Calls made without asking

- Categories have one term per language, not several like tags.
- A post's categories are derived from its public tags. Nothing is stored on `posts`.
- Stage 1 threshold is 0.5. A missed category hides all its tags, a wrong match only costs extra questions.
- A tag with no enabled category is always a stage 2 candidate.
- Only the proposal that creates a tag sets its categories.
- A seed alias's target is made active.
- The admin counts and filters categories on all tags, the public API on public tags.
- Seed tags are `quality`, the default type.
- A seed key that exists as a tag is made active. One that is an alias goes to its target.
- `?category=` repeats with AND semantics, like `?tag=`.
- `GET /categories` is public, so the rebuilt web app can list them.
- Admin work is built on the SPA, not on today's SvelteKit admin.
- The seed list itself: 18 categories, 137 tags, their terms and mapping.

## Left to the builder

- How the seed SQL is laid out: one `VALUES` list per table or a temporary table joined on insert.
- Where category queries live. Suggested: `internal/adapter/postgres/category.go`.
- The exact system prompt wording for `categories`.
- A KB page `docs/kb/categories.md` and its row in `docs/kb/index.md`.
- Tests. At least:
  - the migration applies on an empty database and on one with existing proposed tags and aliases,
  - stage 1 selection, the 0.5 and 0.8 thresholds, uncategorized and disabled-category tags, and no enabled categories,
  - category assignment for new, already categorized, active and untrusted proposals, and unknown keys,
  - merge carries categories,
  - the `category` feed filter with AND, and unknown or disabled keys,
  - each admin route's success and error cases.

## Defaults

| Parameter | Value | Why |
|---|---|---|
| Category threshold | 0.5 | Recall over precision, tags decide precision. |
| Tag threshold | 0.8 | Unchanged. |
| Questions per Jev request | 50 | Unchanged. Covers all categories in one request. |
| Category key | 1 to 40 characters | Same as tag keys. |
| Category term | at most 100 characters | Room for multi-word labels. |
| Cache TTL | 1 minute | Same as tags. |
