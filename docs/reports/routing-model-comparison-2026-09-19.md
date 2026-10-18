# Routing model comparison report

[日本語](routing-model-comparison-2026-09-19_ja.md)

Date: 2026-09-19 (JST)

Dataset: The 20 most recent posts in the production timeline

Models compared: Nemotron 3.5 Lightning / Nemotron 3 Nano / Nemotron 3 Super / Nemotron 3 Ultra / OpenRouter Jev Latest / OpenRouter GPT Luna Latest

This is a record of the experiment at the time it was conducted. References to “current” conditions or a “leading candidate” concern the persona conditions and settings used then. For the current implementation and defaults, see the [technical documentation](../design/technical.md).

## Findings

On these 20 posts, OpenRouter Jev performed best on speed, cost, and adherence to the persona conditions in effect at the time.

- Jev's mean response time was 0.37 seconds, about one fifth of Super's, the next fastest model.
- Jev's estimated cost was approximately JPY 0.026 per post, the lowest of the five models in the initial comparison.
- All five models agreed exactly on 15/20 posts.
- Checking the five disagreements against the persona conditions showed that Jev followed the conditions in all five cases.
- Ultra and Nano each agreed with Jev on 19/20 posts and tied for second in the manual review of disagreements.
- Ultra cost about 17 times as much per post as Jev. Nano averaged 9.26 seconds with thinking enabled and 1.16 seconds with it disabled, but disabling thinking reduced its agreement with Jev to 11/20 posts.
- Lightning was cheaper than Super, but averaged 6.55 seconds in this environment, making it the second slowest model. It also missed some deliveries to `routine`.

Based solely on these 20 posts, Jev was the leading routing candidate, with Super the candidate for retaining the existing Chat Completions interface. Nano was inexpensive and made good decisions with thinking enabled, but was slow; without thinking, it was fast but its decision quality was insufficient. However, this evaluation had no ground-truth labels established in advance and represents a single observation on a small dataset.

## Method

Production Firestore was accessed read-only to extract the 20 most recent user posts. Deleted posts and threads whose root post had been deleted were excluded. The experiment used the production conditions for the six personas active at the time.

Each model received the same posts, persona conditions, and thread context. Threads were cut off at the target post to prevent later replies or answers from influencing the decision. Model call order was rotated for each case to reduce warm-up and execution-order bias.

Nemotron used the same Chat Completions interface and required tool calling as production. Jev is a Decisions model that does not support Chat Completions, so each request contained six binary `deliver` / `exclude` questions, one per persona.

The replay did not activate personas, execute tools, or write to production data. It recorded only model routing decisions and API usage.

## Measurements

### Speed and cost (initial run with thinking enabled)

| Model | Mean | p50 | p95 | Min | Max | Cost for 20 posts | Mean cost per post | Per 1,000 posts |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| OpenRouter Jev Latest | **0.37 s** | **0.28 s** | **0.51 s** | 0.22 s | 1.77 s | **~JPY 0.52** | **~JPY 0.026** | **~JPY 26** |
| Nemotron 3 Super | 1.91 s | 1.79 s | 2.53 s | 1.47 s | 2.77 s | ~JPY 1.96 | ~JPY 0.098 | ~JPY 98 |
| Nemotron 3 Ultra | 2.28 s | 2.03 s | 2.64 s | 1.66 s | 6.48 s | ~JPY 8.98 | ~JPY 0.449 | ~JPY 449 |
| Nemotron 3.5 Lightning | 6.55 s | 5.93 s | 9.28 s | 2.45 s | 16.11 s | ~JPY 1.39 | ~JPY 0.070 | ~JPY 70 |
| Nemotron 3 Nano | 9.26 s | 6.97 s | 22.85 s | 3.94 s | 29.37 s | ~JPY 1.38 | ~JPY 0.069 | ~JPY 69 |

Yen conversions assume USD 1 = JPY 150. They exclude taxes, exchange-rate changes, and future price changes. The initial comparison comprised 100 requests across 20 posts, costing approximately JPY 14.24 in total.

### Token usage

| Model | Input tokens | Output tokens | Total | USD |
|---|---:|---:|---:|---:|
| OpenRouter Jev Latest | 82,680 | 3,560 | 86,240 | $0.003473 |
| Nemotron 3 Super | 30,736 | 4,283 | 35,019 | $0.013076 |
| Nemotron 3 Ultra | 29,943 | 9,969 | 39,912 | $0.059850 |
| Nemotron 3.5 Lightning | 29,383 | 31,316 | 60,699 | $0.009279 |
| Nemotron 3 Nano | 29,243 | 31,145 | 60,388 | $0.009229 |

Jev used more input tokens than Nemotron because its Decisions API format includes a question and conditions for each persona. However, its output token price was zero, and its total cost was the lowest. Lightning and Nano each generated approximately 31,000 output tokens, about 7.3 times Super's output. This was consistent with their longer response times.

### Exact agreement between models

| Pair | Matching posts | Agreement |
|---|---:|---:|
| Lightning ↔ Super | 18/20 | 90% |
| Lightning ↔ Ultra | 16/20 | 80% |
| Lightning ↔ Jev | 17/20 | 85% |
| Nano ↔ Lightning | 18/20 | 90% |
| Nano ↔ Super | 16/20 | 80% |
| Nano ↔ Ultra | 18/20 | 90% |
| Nano ↔ Jev | **19/20** | **95%** |
| Super ↔ Ultra | 16/20 | 80% |
| Super ↔ Jev | 17/20 | 85% |
| Ultra ↔ Jev | **19/20** | **95%** |
| All five models | 15/20 | 75% |

Agreement means that the sets of selected persona IDs were exactly identical. Agreement is not accuracy.

## Analysis of disagreements

All five disagreements concerned whether to include `routine` as a recipient. The summaries below preserve the meaning without reproducing the production posts verbatim.

| Case | Summary | Lightning | Nano | Super | Ultra | Jev | Decision consistent with the conditions |
|---|---|---|---|---|---|---|---|
| P02 | Confirming a bath already taken today and the possibility of another | routine | routine | none | routine | routine | routine |
| P06 | Wanting to study and implement LLMs during free time today | none | routine | none | routine | routine | routine |
| P07 | Wanting to visit a hair salon on the weekend | move | move | move + routine | move + routine | move + routine | move + routine |
| P08 | Going to a specific hair salon on Saturday morning | move + routine | move + routine | move + routine | move | move + routine | move + routine |
| P10 | Wanting to arrive at a destination at a specified time | move | move + routine | move | move + routine | move + routine | move + routine |

The `routine` condition in effect covered both plans and records of completed activities, including timing such as “starting now” or “on the weekend.” Taking that wording literally, consistency across the five disagreements was Jev 5/5, Nano 4/5, Ultra 4/5, Lightning 2/5, and Super 2/5. Nano and Ultra both scored 4/5, but Nano missed P07 and Ultra missed P08.

This was a post-hoc manual review of disagreements only, not an accuracy measurement against a formal ground-truth dataset. The 15 posts on which all models agreed could also contain shared errors.

## Rerun with thinking disabled

Only Lightning and Nano were rerun on the same 20 posts, with `chat_template_kwargs.enable_thinking: false`, `max_tokens: 1024`, and `temperature: 0.1`. On Token Factory, a top-level `enable_thinking` setting did not disable thinking; it had to be specified inside `chat_template_kwargs`.

| Model | Mean | p50 | p95 | Max | Cost for 20 posts | Agreement with Jev |
|---|---:|---:|---:|---:|---:|---:|
| Lightning (thinking disabled) | 1.40 s | 1.36 s | 2.01 s | 2.03 s | ~JPY 0.44 | 11/20 (55%) |
| Nano (thinking disabled) | **1.16 s** | **1.14 s** | **1.36 s** | **1.45 s** | **~JPY 0.39** | 11/20 (55%) |

Speed improved substantially. Nano's mean fell from 9.26 to 1.16 seconds, and Lightning's from 6.55 to 1.40 seconds. Output tokens fell from approximately 31,000 each to 3,611 and 4,866, respectively. However, both models' agreement with Jev dropped to 11/20.

Checking all nine Nano–Jev disagreements against the persona conditions showed that Jev followed the conditions in every case. Nano made the following errors:

- Excluded `routine` from explicit plans or activity records involving bathing, studying that day, a hair salon visit, a desired arrival time, and taking medication every morning.
- Routed a complaint about a matchmaking service to `routine`, outside its scope.
- Routed daily morning medication to `purine-checker`.
- Routed a report that a microphone problem had been resolved to `move`.

Lightning also omitted `routine` from posts about bathing, studying, a hair salon visit, arrival time, medication, and starting work, and omitted `wine-assistant` from one wine image. During the rerun, one response failed to complete a valid tool call within 1024 tokens, but a retry of that case completed.

Nano therefore offered good decision quality but slow responses with thinking enabled, and fast responses but insufficient routing quality with it disabled. Under these conditions, it was not a substitute for Jev.

## Additional measurement: OpenRouter GPT Luna Latest

`~openai/gpt-luna-latest` was tested on the same 20 posts through OpenRouter Chat Completions. It used the same required tool calling as the existing router, with Exacto selected as the OpenRouter provider and reasoning enabled. OpenRouter's listed prices at the time were $0.20/M input tokens and $1.20/M output tokens.

| Model | Mean | Input | Output | Cost for 20 posts | Cost per post (USD 1 = JPY 150) | Agreement with Jev | Agreement with Super |
|---|---:|---:|---:|---:|---:|---:|---:|
| GPT Luna Latest | 3.65 s | 21,424 | 3,467 | $0.008445 (~JPY 1.27) | ~JPY 0.063 | **19/20 (95%)** | 16/20 (80%) |
| GPT Luna Latest (thinking disabled) | **2.02 s** | 21,424 | 2,587 | $0.007389 (~JPY 1.11) | **~JPY 0.055** | **19/20 (95%)** | 18/20 (90%) |

With thinking enabled, the only disagreement with Jev was P20. For a short tasting comment about wine, Jev selected only `wine-assistant`, while Luna selected both `wine-assistant` and `purine-checker`. The condition for `purine-checker` concerned assessing purines from dish names, menus, or food photos, so the extra delivery did not follow the condition. Luna agreed with Jev on all five earlier disagreements (P02, P06, P07, P08, P10), following the conditions in effect.

Decision quality was close to Jev's on these 20 posts, but response time was about 9.9 times Jev's and 1.9 times Super's. Cost was about 2.4 times Jev's, but approximately 65% of Super's. It was therefore an OpenRouter candidate for prioritizing decision quality while retaining Chat Completions and explanatory reasons. Considering speed and cost as well, Jev remained the leading router candidate, and Super remained a reasonable Token Factory default.

Disabling thinking reduced the mean to 2.02 seconds (p50 1.85 seconds, p95 2.83 seconds, maximum 5.02 seconds) and lowered cost by about 13%. Agreement with Jev stayed at 19/20, but the disagreement changed. P20, misrouted with thinking enabled, now matched Jev, while P06—wanting to study and implement something during free time today—lost `routine`. Since the condition covered plans that included timing, this did not follow it. No collapse in decision quality was observed on these 20 posts when thinking was disabled. For Luna, disabling it was better on speed and cost, but still averaged about 5.5 times Jev's response time and roughly matched Super's speed.

## Observations by model

### OpenRouter Jev Latest

It was the fastest and cheapest on this dataset and followed the persona conditions most closely in the disagreement cases. The Decisions API was well suited to routing and classification. However, it could not use the existing OpenAI-compatible Chat Completions client unchanged; an adapter for its dedicated API would be needed. Also, `jev-latest` is an alias for the latest version, so updates could change its behavior.

### Nemotron 3 Ultra

It agreed with Jev on 95% of posts, and the manual review found that it followed the conditions in four of the five disagreements. It could retain the existing tool-calling interface, but was the most expensive of the initial five models.

### Nemotron 3 Super

At a mean of 1.91 seconds, it was the fastest Nemotron model in the initial run and cost less than Ultra. However, it tended not to include `routine` for travel with a specified time or activities planned for the same day, following the conditions in only two of the five disagreement cases.

### Nemotron 3.5 Lightning

Its token prices were low, but at a mean of 6.55 seconds, it was the second slowest in this experiment. It generated many output tokens and did not deliver the low latency its name might suggest. Its decisions agreed with Super on 90% of posts, but it missed some cases that matched multiple conditions, such as excluding `routine` from a weekend hair salon plan.

### Nemotron 3 Nano

With thinking enabled, it agreed with Jev on 95% of posts, and the manual review found that it followed the conditions in four of the five disagreements. Its input and output prices were the same as Lightning's, and the 20-post cost was low at approximately JPY 1.38. However, it was the slowest of the initial five models, with a mean of 9.26 seconds, p95 of 22.85 seconds, and maximum of 29.37 seconds. Disabling thinking improved its mean to 1.16 seconds, but agreement with Jev dropped to 55%, and misrouting occurred.

## Recommendations

The next step was to evaluate Jev as the leading candidate using more data and ground-truth labels.

1. Create a fixed evaluation set of approximately 50–100 posts with personal information removed.
2. Manually label the expected `deliver` / `exclude` decision for each persona.
3. Measure per-persona precision and recall, especially missed deliveries, alongside exact recipient-set agreement.
4. Compare a pinned version as well if avoiding the effects of Jev Latest updates is necessary.
5. Add failure fallbacks and monitoring for the Decisions API before switching production.

At the time of this experiment, Jev was the leading candidate on the balance of speed, cost, and quality. For a default that retained the existing API, Super was reasonable: faster than Lightning and more reliable than Nano with thinking disabled. Nano was slow with thinking enabled and lacked sufficient quality without it. These results also did not support choosing Lightning for routing.

## Reproduction and data handling

The comparison code is in `internal/persona/route_production_test.go`. It does not run as part of normal tests; it requires an explicit production-replay option and a key file. Detailed results, including each of the 20 posts, model outputs, usage, and execution time, were saved to `/tmp/kakeratta-production-routing.json` with permissions `0600`.

This detailed JSON contains production post text and must not be added to the repository. Key files are also excluded by `tmp/*.key` in `.gitignore`.

The following token prices were used for this experiment's cost calculations.

| Model | Input / million tokens | Output / million tokens |
|---|---:|---:|
| Lightning | $0.06 | $0.24 |
| Nano | $0.06 | $0.24 |
| Super | $0.30 | $0.90 |
| Ultra | $1.00 | $3.00 |
| Jev | $0.042 | $0.00 |

For Jev, the API response's `usage.cost` was preferred as the measured cost.
