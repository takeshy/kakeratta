# Routing model comparison — 2026-09-17

Local experiment only. Production model selection and deployments were unchanged.

The test calls the production Route function with the existing prompt, tool schema,
four built-in persona domains, and 12 synthetic Japanese cases. Cases cover single
domains, no matching domain, misleading keywords, multiple domains, thread
follow-ups, time corrections, and topic changes. Expected recipients were specified
before running either model. No persona responses, reviews, memory, search, vision,
or scheduled tasks were executed.

| Model | Exact recipient matches | Input tokens | Output tokens | Total USD | USD/route | JPY/route at assumed 150 JPY/USD | Mean latency |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Lightning | 12/12 | 17,990 | 11,019 | 0.003724 | 0.000310 | 0.0465 | 3.18 s |
| Ultra | 12/12 | 18,326 | 2,919 | 0.027083 | 0.002257 | 0.3385 | 1.68 s |

Costs use API-reported prompt_tokens and completion_tokens and the user's prices
per million tokens: Lightning $0.06/$0.24 input/output, Ultra $1.00/$3.00.
These are list-price estimates, not billing reconciliation. Yen figures assume
150 JPY/USD, not a fetched exchange rate. Both models were run once per case.

Routing cost fell approximately 86.2% (7.27 times cheaper), saving about 0.292 yen
per route in this sample. Lightning generated more output tokens and was slower.
The entire 24-call experiment cost approximately $0.030807, or 4.62 yen at that
assumed exchange rate.

The reported production expense of $1.05 for 30–40 posts corresponds to
3.94–5.25 yen/post at the same assumed exchange rate. The target of 0.5 yen/post
requires about 87–90% total savings. This routing sample alone cannot establish
total savings: actual threads, custom personas, multiple deliveries, workflow
reviews, tool rounds, listening turns, images, and background activity differ.
If production routing were comparable to this sample, replacing routing alone
would save only about 0.29 yen/post, insufficient for the overall target.

The next useful measurement is per-stage token usage for a complete post in an
isolated workspace, separating routing, persona execution, review, and listening.
Twelve synthetic cases passing once is evidence for a pilot, not a production
accuracy guarantee. Custom domains, longer threads, and repeated trials remain
unmeasured.

## Reproduce

From the repository root, with a raw API key in a file:

```sh
KAKERATTA_ROUTE_LIVE=1 KAKERATTA_ROUTE_KEY_FILE="$PWD/temp/token_factory.key" go test ./internal/persona -run '^TestRouteLiveComparison$' -count=1 -v -timeout=10m
```

This explicitly makes 24 paid API calls. Ordinary tests skip this experiment.
NEBIUS_API_KEY or KAKERATTA_ROUTE_SETTINGS (a Nebius settings JSON file) can also
supply credentials. Never commit the key. The run log for this experiment is
/tmp/kakeratta-route-comparison.log.

## Production-data replay

Read-only replay against the current production workspace, using its 6 active
personas and the latest 20 non-deleted user posts/replies among 75 saved entries.
Each model received the same current saved thread prefix through the trigger.
Saved image descriptions/OCR were included; images were not reprocessed.
Historical versions of edited posts and historical persona definitions are not
available, so this is not an exact reconstruction of the original requests.
Only Route was called: explicit-mention overrides and previous-recipient edit
notifications were outside the comparison.

| Model | Input tokens | Output tokens | Estimated USD, 20 calls | JPY/route at assumed 150 JPY/USD | Mean latency |
| --- | ---: | ---: | ---: | ---: | ---: |
| Lightning | 37,473 | 28,822 | 0.009166 | 0.0687 | 4.99 s |
| Ultra | 38,033 | 7,568 | 0.060737 | 0.4555 | 1.82 s |

Recipient sets agreed on 17/20 cases (85%). This is model agreement, not accuracy.
There were two semantic disagreements: Lightning omitted a custom image-related
food persona and omitted the routine persona for an immediate planned activity.
Review of the supplied domains supports Ultra's selections in these two cases.
The third disagreement was a malformed Lightning response: 8,192 output tokens,
27.13 seconds, and no usable delivery decision. Ultra returned the routine persona.

Before the completed comparison, another Lightning request produced a malformed
response and stopped the initial test. Four completed pairs were checkpointed;
the interrupted case was rerun and succeeded. Thus 41 requests were made in total:
21 Lightning (2 malformed responses observed) and 20 Ultra (none observed).
The table covers the 40 calls in the completed paired dataset. The earlier failed
request's usage was not checkpointed, so $0.069903 (about 10.49 yen) is the recorded
paired-run cost, not the full experiment bill. Do not treat 17/20 as the success
rate of all attempts, or treat this small sample as a stable failure-rate estimate.

For the paired dataset, routing was about 84.9% cheaper on Lightning, saving about
0.387 yen per call, but Lightning was slower and had missed deliveries and malformed
output. A direct production switch is not supported by this result alone.
Useful next experiments are prompt/tool-output reliability improvements and a
fallback on invalid responses, followed by replaying the same cases. A fallback
cannot detect a valid but incorrect empty recipient list.

Private review artifacts (not committed):
- /tmp/kakeratta-production-routing-context.json — current domains and saved entries
- /tmp/kakeratta-production-routing.json — per-case decisions, usage and errors
- /tmp/kakeratta-production-routing.log — initial run
- /tmp/kakeratta-production-routing-resume.log — continuation

Run with KAKERATTA_ROUTE_PRODUCTION=1 and KAKERATTA_ROUTE_KEY_FILE, selecting
TestRouteProductionComparison. It requires gcloud authentication for read-only
Firestore access. If more than one user exists, KAKERATTA_ROUTE_USER must explicitly
select the user document. KAKERATTA_ROUTE_INSPECT_ONLY=1 fetches a private local
review snapshot without LLM calls. KAKERATTA_ROUTE_RESUME=1 skips completed pairs
from the private result checkpoint; only use it while source data and settings
remain unchanged. Ordinary tests skip the production replay.

## Exclusion-reason experiment

A test-only HTTP request rewrite replaced selected-persona explanations with a
required excluded_reasons map and instructed the model to account for every
persona, without changing the production prompt or tool schema. Lightning replayed
the same saved 20-case snapshot; the existing Ultra results were reused as a
comparison, not treated as gold labels. No fresh Firestore read was needed.

| Lightning variant | Agreement with prior Ultra | Malformed responses in paired 20 cases | JPY/route at 150 JPY/USD | Mean latency |
| --- | ---: | ---: | ---: | ---: |
| Selected reasons (previous run) | 17/20 | 1 | 0.0687 | 4.99 s |
| Excluded reasons | 13/20 | 0 | 0.0871 | 6.59 s |

Excluded-reason run: 39,433 input tokens, 38,522 output tokens, $0.011611 total
(about 1.74 yen). Every non-selected persona had an exclusion reason, and all
responses were usable in this run. These are single runs, so the absence of
format errors does not establish a reliability fix.

Explanations exposed invented restrictions: the food-image specialist was
excluded because no explicit analysis was requested; routine handling was
excluded because immediate or weekend timing was not a precise clock time;
the book specialist was excluded from a question about a book because the
question was not a review or quotation. One changed decision to include routine
may itself be reasonable; lower agreement alone is not an accuracy score.
However, the existing food-image and immediate-action omissions were not fixed,
and new book-related omissions appeared.

Requiring exclusion reasons is useful for diagnosing missed deliveries, but this
version was slower, costlier, and did not improve the known omissions. A next
experiment should clarify that domain examples are non-exhaustive and explicit
requests or exact clock times are not prerequisites unless the domain says so.
Do not switch production based on this experiment.

Test: TestRouteExclusionReasons, enabled with KAKERATTA_ROUTE_EXCLUSION=1 and
KAKERATTA_ROUTE_KEY_FILE. Private artifacts:
- /tmp/kakeratta-routing-exclusion.json
- /tmp/kakeratta-routing-exclusion.log

## Condition-matching experiment

Two further Lightning-only replays used the same saved 20 cases. The prompt framed
the task as matching post content against each persona's conditions, avoiding
request/recipient-selection framing. It retained full coverage of matched IDs and
non-match reasons. Production code and persisted persona definitions were unchanged.

1. **Classifier wording only:** original domain text, but a condition-matching
   system prompt, list heading and tool descriptions.
2. **Condition definitions rewritten:** same classifier, with the six domain
   descriptions manually rewritten as predicates about post content in the test.

| Variant | Agreement with prior Ultra | Malformed responses | JPY/route at 150 JPY/USD | Mean latency |
| --- | ---: | ---: | ---: | ---: |
| Classifier wording only | 15/20 | 0 | 0.0582 | 4.12 s |
| Rewritten conditions | 16/20 | 0 | 0.0585 | 4.21 s |

Wording-only: 34,073 input / 23,816 output tokens, $0.007760.
Rewritten conditions: 30,033 input / 24,981 output tokens, $0.007797.
The two additional runs cost about $0.015558 (2.33 yen at the assumed exchange rate).

The wording-only version still invented a need for an explicit purine-analysis
request. Rewriting the conditions fixed the observed food-photo omission and the
book-content question omissions; it also selected routine for immediate and
weekend actions. All 20 responses included complete non-match explanations.

Four rewritten-condition decisions differed from the earlier Ultra baseline:
- An action-completion/repetition statement matched routine.
- A broad intention to study/implement something matched routine.
- A named destination/time follow-up matched routine but omitted move; its reason
  incorrectly required route details, indicating a remaining missed match.
- A destination/arrival-time statement matched both move and routine.

These differences cannot all be called errors: some follow naturally from the
rewritten conditions, and Ultra is not ground truth. The experiment changed the
domain semantics as well as wording: notably the purine condition now explicitly
includes text questions, the book condition includes questions about book content,
and routine's scope is more explicit. Therefore improvements cannot be attributed
solely to avoiding request framing. The rewritten conditions were informed by the
same dataset, so this is development-set evidence and needs held-out validation.

The rewritten version's routing estimate is about 87.2% below the prior Ultra
baseline (0.4555 yen/route), saving about 0.397 yen/route. This still does not imply
0.5 yen for the entire post-processing pipeline. No production switch was made.

Reproduce with TestRouteConditionMatching, KAKERATTA_ROUTE_CONDITIONS=1 and
KAKERATTA_ROUTE_KEY_FILE; add KAKERATTA_ROUTE_NORMALIZED_CONDITIONS=1 for the
rewritten definitions. Definitions and prompt are in route_exclusion_test.go.
Private artifacts:
- /tmp/kakeratta-routing-conditions.json and .log
- /tmp/kakeratta-routing-normalized-conditions.json and .log

## Revised routing text and persona creation

The local application now drafts domain fields as content-matching conditions.
Nebius persona creation/editing explicitly selects Nemotron Ultra independently of
the persona execution tiers; other compatible endpoints keep the existing
high/middle/low fallback. UI labels now say matching conditions. Existing saved
personas are not automatically rewritten, and routing's model selection is unchanged.

The routing prompt now checks all available text, image descriptions and source
metadata, interprets action-oriented legacy domains as content conditions, and
warns against inventing prerequisites. It explicitly addresses destination-only
follow-ups and distinguishes concrete plans/completion records from vague hopes.
Non-match explanations are requested and logged under excluded_reasons.

A Lightning replay with the same manually rewritten conditions and revised prompt
agreed with the earlier Ultra output in 17/20 cases:
35,313 input tokens, 22,851 output tokens, $0.007603 total,
0.0570 yen/route at 150 JPY/USD, 3.92 seconds mean latency.
There were no malformed delivery responses, but two explanation maps used
display-name suffixes rather than exact persona IDs, so coverage validation failed
for those explanations. Delivery IDs remained usable.

The broad study/implementation aspiration was no longer routed to routine.
Remaining differences: routine selected for an action-completion/repetition
statement; named-destination follow-up still omitted move; arrival-time statement
selected both move and routine. The destination omission remains a known issue.
No claim of full routing accuracy or full-post cost attainment is warranted.

This replay used manually rewritten conditions, not freshly generated Ultra
conditions, and compared against the old Ultra output. It does not measure
end-to-end persona generation quality or equal-prompt inter-model agreement.
Tests for model selection and existing persona/server behavior pass, as do the
21 JavaScript tests. No deployment or production configuration write was made.

Private replay artifacts: /tmp/kakeratta-routing-revised.json and .log.
Reproduce via TestRouteConditionMatching with KAKERATTA_ROUTE_CONDITIONS=1,
KAKERATTA_ROUTE_NORMALIZED_CONDITIONS=1, KAKERATTA_ROUTE_REVISED=1 and the key-file
variable. As before, this is a paid opt-in development-set replay.

## User-only thread ablation

For the named-destination follow-up (case 13), ran Lightning three times with the
same revised prompt and rewritten conditions in each of two variants. The only
request-content change was filtering persona messages from the thread before
rendering it. These are six fresh calls, not comparisons of a new call with an old
single baseline. Runs were not interleaved; three repetitions are exploratory.

| Thread context | Included move | Actual outputs | Estimated USD, 3 calls | Mean latency |
| --- | ---: | --- | ---: | ---: |
| User statements only | 2/3 | move+routine, move+routine, routine | 0.001617 | 6.61 s |
| User and persona statements | 1/3 | move+routine, routine, routine | 0.001489 | 5.82 s |

No malformed responses occurred. The old Ultra result was move only, so exact
agreement was 0/3 in both variants; for this question the relevant metric is whether
the missing move recipient is restored. Removing persona text restored it in two
trials but still failed once. Both variants still sometimes invented a need for
route/travel-time details despite the explicit destination-follow-up instruction.
This does not establish persona text as the cause or justify a global user-only
routing change. Total estimated cost was $0.003106 (about 0.47 yen at 150 JPY/USD).
Production thread selection was unchanged.

Test controls: KAKERATTA_ROUTE_CASE=13 and KAKERATTA_ROUTE_REPEAT=3, in addition to
the revised-condition replay flags. Add KAKERATTA_ROUTE_USER_ONLY=1 to filter
persona statements. Private artifacts: /tmp/kakeratta-routing-case-user-only.json
and .log; /tmp/kakeratta-routing-case-full.json and .log.

## Short michi condition, full thread

Tested the final shortened built-in move domain, with all persona statements
retained through the trigger. All other conditions and the revised router prompt
were kept as in the prior experiment.

Problem case 13, three fresh calls: move+routine in all 3/3.
Estimated cost $0.001273 total, mean 4.37 seconds. This restores the previously
missing move recipient, though it is not an exact match to the old Ultra move-only
output and three trials do not prove reliability.

A separate full 20-case replay also selected move+routine for case 13. Overall
agreement with the old Ultra baseline was 17/20. The three differences were:
- A vague study/implementation aspiration went to routine.
- An unnamed hair-salon/weekend statement went to move instead of routine.
- The named-salon follow-up went to move+routine instead of move alone.

The second difference reveals a remaining issue: the shorter condition permits
the model to treat a generic facility type as a sufficiently specific destination.
The original missed named-destination follow-up improved, but routing is not fully
resolved. The first difference also illustrates run-to-run variability.

20-case totals: 35,153 input tokens, 24,224 output tokens, $0.007923,
0.0594 yen/route at 150 JPY/USD, mean 4.09 seconds. No malformed delivery responses;
6 explanation maps failed exact-ID coverage validation (delivery results still
parsed). Combined 23-call estimated cost: $0.009196, approximately 1.38 yen.
No deployment or production data write.

Private artifacts:
- /tmp/kakeratta-routing-move-short-case.json and .log
- /tmp/kakeratta-routing-move-short-all.json and .log

Reproduce the revised-condition replay with KAKERATTA_ROUTE_MOVE_PROPOSAL=1
and leave KAKERATTA_ROUTE_USER_ONLY unset. For the three repetitions, also set
KAKERATTA_ROUTE_CASE=13 and KAKERATTA_ROUTE_REPEAT=3.

## Compact conditions for all personas

Shortened every active persona's condition to one or two positive sentences
(built-in domains from `Builtins`; purine-checker and wine-assistant rewritten in
the test), kept the revised router prompt and full thread. Lightning, same saved
20 cases, three separate full runs. Old Ultra output is still a comparison, not gold.

| Run | Agreement with prior Ultra | Malformed / unusable | Input / output tokens | USD | JPY/route@150 | Mean latency |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 15/20 | 0 | 24,393 / 21,312 | 0.006578 | 0.0493 | 3.57 s |
| 2 | 15/20 | 2 dropped deliveries | 25,093 / 23,878 | 0.007236 | 0.0543 | 3.98 s |
| 3 (persona-id wording) | 15/20 (16/20 after resolver fix) | 1 dropped delivery | 25,753 / 33,551 | 0.009597 | 0.0720 | 5.44 s |

Compact conditions cut input tokens by about 30% compared with the 35,153 of the
short-michi run, so the cost per route was the lowest measured so far in runs 1–2.

**Run 2 format failure.** The prompt said "全idをどちらか一方に含めます". Thread
entries are rendered with their post IDs, and in 5/20 responses the model keyed
excluded_reasons by the trigger's post ID instead of persona IDs. In cases 2
(a shochu photo) and 5 (a book-page image) it also returned an empty delivery,
dropping gourmet and book respectively. Run 1 made the same cases correctly, so
this is run-to-run instability triggered by an ambiguous word.

**Fix 1 (prompt).** The instruction now says 担当id and explicitly states that it
is the ID at the start of each condition line, not a post ID. Run 3 had no post-ID
keys and cases 2 and 5's topics were recognised.

**Run 3 format failure.** Four responses instead copied the display name,
e.g. `book（shiori）`. In case 5 that key appeared in deliver, did not resolve,
and book was dropped. **Fix 2 (code).** Route's ID resolver now strips a trailing
full-width or ASCII parenthetical before matching, with a unit test. Re-scoring
run 3's saved output with that resolver makes case 5 deliver book: 16/20. That
figure is a re-score, not a fresh call.

Remaining differences from Ultra in run 3 (after the resolver fix):
- Case 7 (did I bathe today / maybe bathe again) → routine; Ultra chose nobody.
  Plausibly matches the new routine condition.
- Case 11 (vague intention to study/implement LLMs today) → routine. Recurring
  across runs; the "今から/週末 etc. count" wording may be too broad.
- Case 12 (weekend hair salon) → move+routine, and case 13 (named salon on
  Saturday morning) → move+routine. Both follow the new move condition, which
  intentionally accepts a type of place; the michi instructions ask for the shop
  name. Ultra's single choices were made under the old conditions.

Three runs show considerable variance (case 3 purine-checker, 7 routine, 2/5
dropped) for identical input; single-run agreement should not be compared closely.
Output tokens vary by about 1.6× between runs because reasoning length varies.
Total for these three runs: $0.023411 (about 3.51 yen at 150 JPY/USD).
No deployment or production data write.

Private artifacts:
- /tmp/kakeratta-routing-compact-first.json, /tmp/kakeratta-routing-compact.log — run 1
- /tmp/kakeratta-routing-compact-second.json, /tmp/kakeratta-routing-compact-final.log — run 2
- /tmp/kakeratta-routing-compact-trigger-id.json and .log — run 3

Reproduce with KAKERATTA_ROUTE_COMPACT=1 in addition to the revised-condition
flags (KAKERATTA_ROUTE_CONDITIONS, _NORMALIZED_CONDITIONS, _REVISED, key file).

## Final run

Reported by the author after the runs above, with the final production
configuration (compact conditions, `excluded_reasons` required, no examples in
the prompt): Lightning agreed with the earlier Ultra output on 18/20 cases. The
two remaining disagreements were immediate-action posts of the case 7 kind
(bathing today / maybe again), which Lightning routed to routine and Ultra to
nobody. Both were reviewed by hand and judged acceptable either way. This result
is recorded as reported; it was not reproduced in this document's replays, and
the artifacts of the run were not kept.

