# AI cost after 06:30 and tuning — October 7, 2026

[日本語](./production-ai-cost-2026-10-07_ja.md)

Snapshot: October 7, 2026, 06:30–13:59:21 JST, retrieved around 14:06. Production inference logs contain 112 calls, estimated at **$0.224126** using observed tokens and standard prices. Three infographic turns cost **$0.063724**; their model, output limit and format remain unchanged.

These are estimates, not provider bills. Cache/contract discounts, credits, search, infrastructure, development requests and direct external-agent requests are excluded. Reasoning/cache counts are breakdowns, not extra tokens. CSV metadata is in `/tmp/kakeratta-ai-cost-after0630-2026-10-07.csv`; raw snapshots are uncommitted.

## Persona and stage costs

The Japanese display labels below refer to the same personas. 記憶更新・まとめ combines memory updates and summaries; 共通処理 denotes shared work.

| 担当 | 呼び出し | 調査 | 回答 | 予定計画 | 記憶更新・まとめ | review | 合計USD |
|---|---:|---:|---:|---:|---:|---:|---:|
| インフォグラフィック | 6 | 0.007865 | 0.055859 | 0.000000 | 0.000000 | 0.000000 | 0.063724 |
| tabe | 26 | 0.034288 | 0.003887 | 0.003533 | 0.000000 | 0.000000 | 0.041708 |
| sona | 15 | 0.011751 | 0.006295 | 0.006738 | 0.001571 | 0.001047 | 0.027402 |
| ai-qa | 7 | 0.004843 | 0.012923 | 0.003888 | 0.000000 | 0.000000 | 0.021653 |
| 父・森田三夫 | 12 | 0.006051 | 0.003367 | 0.003656 | 0.003575 | 0.000000 | 0.016650 |
| shiori | 11 | 0.004926 | 0.002808 | 0.005282 | 0.000000 | 0.000000 | 0.013016 |
| goldfish-care | 3 | 0.001887 | 0.001056 | 0.001147 | 0.000000 | 0.000000 | 0.004090 |
| wine-assistant | 3 | 0.001798 | 0.001063 | 0.001174 | 0.000000 | 0.000000 | 0.004035 |
| recipe | 3 | 0.001746 | 0.001014 | 0.001160 | 0.000000 | 0.000000 | 0.003920 |
| move | 3 | 0.001692 | 0.000969 | 0.001024 | 0.000000 | 0.000000 | 0.003685 |

Shared costs outside the persona table: monthly summary $0.022409 (one call), routing $0.001679 (two), embedding $0.000153 (20).

| 処理 | 呼び出し | 入力tokens | 出力tokens | 概算USD |
|---|---:|---:|---:|---:|
| research | 41 | 241979 | 4727 | 0.076848 |
| answer | 22 | 87918 | 17415 | 0.089242 |
| plan_schedule | 21 | 84740 | 2423 | 0.027603 |
| memory_update | 3 | 10563 | 1337 | 0.004372 |
| memory_summary | 1 | 2010 | 189 | 0.000773 |
| review | 1 | 2782 | 236 | 0.001047 |
| routing | 2 | 3864 | 578 | 0.001679 |
| monthly_summary | 1 | 50121 | 8192 | 0.022409 |
| embedding | 20 | 15287 | 0 | 0.000153 |

## Per-post costs

Calls are grouped by persona and trigger ID. Start time is the first call observed in this window. Routing/embedding are excluded here; periodic wakes without trigger IDs are included in the persona table.

| 開始時刻JST | 担当 | 対象投稿ID | 呼び出し | 概算USD |
|---|---|---|---:|---:|
| 06:38:38 | インフォグラフィック | local-a98eb263-b3f5-4ffe-b85a-bce785a320d7 | 2 | 0.022370 |
| 07:03:42 | インフォグラフィック | local-507c0890-b138-4507-9f12-5e668c4cd47e | 2 | 0.021533 |
| 11:03:38 | ai-qa | local-424826dc-560b-4408-be8f-b1b2b91c7284 | 4 | 0.005526 |
| 11:25:27 | 父・森田三夫 | local-64283fd3-408e-4a30-a31c-ea27d14d8aba | 4 | 0.005443 |
| 11:26:19 | 父・森田三夫 | local-93380b7f-53d3-4343-a6fb-3901bf8e39b4 | 4 | 0.005870 |
| 12:06:45 | ai-qa | local-b9166968-cb49-463b-88eb-437dde3e5d95 | 3 | 0.016127 |
| 13:39:21 | インフォグラフィック | local-b2fa1013-a8ba-4daf-b9ca-8596c3240589 | 2 | 0.019822 |
| 13:58:55 | sona | local-2ebbc8a3-1829-4a53-be50-18a5d959e3ba | 6 | 0.009350 |

## Findings and implemented changes

All three tabe check-ins were silent, but cost $0.034288 for 20 research requests, $0.003887 for three empty answers and $0.003533 for three empty schedule plans. At 07:53, ten period reads used the ID-only `entries` format; at 10:30, eight did so. Every one received the same missing-ID refusal. The correct period lookup is `type: tool, tool: timeline_search`.

Matching persona and time against 18 workflow logs identified 12 silent check-ins whose empty answer calls cost $0.013325. Schedule planning ran 21 times; the retrieved workflows contain one nonempty schedule plan from sona. Empty plans alone do not prove a call unnecessary. Default shiori/tabe definitions lack schedule permission, yet their eight schedule calls cost $0.008816. Saved custom definition permissions were not retrieved.

Implemented:

- Gate schedule planning and review regeneration on schedule permission, independently of memory. Keep permitted actions and post-publication memory work.
- Offer `finish_silent` only to daily/monthly system check-ins without due record reviews. After necessary reads, silence skips answer composition while preserving planning, execution and wake bookkeeping. Normal posts, explicit schedules, record-review questions and external-agent protocols retain their existing paths.
- Validate ID reads before initialization/lookup, supply the correct period-search format when available, and apply the existing two-correction limit after initial planning as well. Three malformed responses end research with an error; missing records are not treated as completion or absence of change.
- Explore older tag records only for a concrete connection to new relevant activity; otherwise stop when no relevant change, approaching plan or due review exists.

Research now advertises each tool with its own object argument schema. ID reads require IDs, timeline_search requires a query or date, and instructions include a period-only search example. Historical JSON-string reads and persisted workflows remain readable.

No model tiers or infographic output settings changed. Changes are not deployed.

## Conditional savings

If the 12 observed silent check-ins choose the new path, empty-answer savings are $0.013325. If the eight shiori/tabe turns use their default permissions, schedule savings add $0.008816. Holding observed tokens fixed, $0.022141 is 9.9% of the window total or 13.8% excluding infographics. This is a conditional estimate, not measured post-deployment savings.

For the two malformed period-read loops, observed research rounds after round 3 cost $0.022847. If malformed responses recur from the start, the new bound stops after three responses. Successful recovery and changed contexts alter usage, so this is reference cost, not a guaranteed additional saving. Do not add empty-answer savings for a turn that instead stops on an error.

## Evidence and validation

The existing 210-record VM snapshot yielded 112 calls after the cutoff. Another read-only query fetched 18 workflows in the same window. Neither retrieval limit was reached. All 112 calls are Nebius success records with complete token counts and zero retryCount. One Super monthly_summary hit length at 8,192 output tokens; that is separate from infographic answer generation.

Prices per million input/output tokens: Super $0.30/$0.90, Ultra $1/$3, embedding $0.01/$0, matching the earlier report. [Nebius pricing](https://nebius.com/services/token-factory/models/nvidia-nemotron-models-inference) and the [official Ultra description](https://github.com/nebius/token-factory-cookbook/blob/main/models/nemotron/nemotron3-ultra-550b-a55b.md) were rechecked this session.

[cost_test.go](../internal/persona/cost_test.go) verifies silent scheduling, retained read-only answers/memory, invalid silence rejection, bounded malformed period reads and recovery to a valid search. Local tests use fake completions; real-model quality and savings remain unmeasured. No paid inference or production changes were performed.

Validation result: `go test ./...` passed across all packages and `git diff --check` passed after the final changes, including silent scheduling, retained due-review questions, bounded malformed reads and recovery.

Structured-argument follow-up: [structured_reads_test.go](../internal/persona/structured_reads_test.go) verifies the actual inference schema, period/ID execution, legacy restoration and preserved MCP local references without mutating declarations.
