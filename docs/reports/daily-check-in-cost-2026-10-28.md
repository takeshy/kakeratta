# Daily check-in cost: Super VL and Super — October 28, 2026

The daily run was changed from **Super VL to Super** in commit `a4b4485` and deployed by Cloud Build `b6566fbe-7738-4bad-957a-488ebd2245a0`. A requested production rerun completed successfully for all nine active remembering personas. The estimated cost fell from **US$0.06143940 to US$0.02488740** in these two observations, about **59.5% lower**. At an illustrative US$1 = JPY150, these amounts are **JPY9.22 and JPY3.73**.

This is a same-day before/after observation, not a controlled benchmark or a guaranteed saving. The rerun followed the first run's memory changes and intervening user activity, and its input tokens differ. No answer-quality equivalence was measured.

## Scope and evidence

Both observations cover the application-managed daily work for one production workspace: nine persona check-in answers, one shared daily summary, one memory-tidying call, and one memory-update call. Each has 12 distinct `insertId` values, all with successful outcomes and reported input/output usage. Neither query reached its result limit. Private persona names, entry IDs, workspace identifiers and raw logs are omitted.

| Measure | Original run | Requested rerun |
| --- | ---: | ---: |
| Model | `nvidia/Nemotron-3.5-Super-VL` | `nvidia/nemotron-3-super-120b-a12b` |
| Observed calls, UTC | 08:45:28–08:48:50 | 09:16:21–09:16:58 |
| Observed calls, JST | 17:45:28–17:48:50 | 18:16:21–18:16:58 |
| Personas | 9 | 9 |
| Calls, including shared summary and memory work | 12 | 12 |
| Input tokens | 78,495 | 71,627 |
| Output tokens, including reasoning | 42,101 | 3,777 |
| Reasoning tokens, already included in output | 40,946 | 2,443 |
| Estimated USD | 0.06143940 | 0.02488740 |
| Illustrative JPY at 150/USD | 9.22 | 3.73 |

The original output was about 97.3% reasoning tokens. Both models have the same rate in the application's [rate table](../../internal/usage/usage.go): US$0.30 per million input tokens and US$0.90 per million output tokens, referenced to [Nebius Nemotron pricing](https://nebius.com/services/token-factory/models/nvidia-nemotron-models-inference). The observed saving comes from fewer tokens, predominantly reasoning output, rather than a lower price per token.

## Cost by purpose

| Purpose | Calls in each run | Original estimated USD | Rerun estimated USD |
| --- | ---: | ---: | ---: |
| `answer` | 9 | 0.04484100 | 0.01862520 |
| `daily_summary` | 1 | 0.00418500 | 0.00244110 |
| `memory_tidy` | 1 | 0.00722580 | 0.00229470 |
| `memory_update` | 1 | 0.00518760 | 0.00152640 |
| **Total** | **12** | **0.06143940** | **0.02488740** |

For one person persona, its own daily answer and memory work totalled US$0.02182560 before and US$0.00671580 after, approximately JPY3.27 and JPY1.01 at the same illustrative exchange rate. These are that persona's totals; they exclude the shared summary and the other eight personas.

## Reproducing the accounting

Retrieve `path="model-usage"` records from both Kakeratta VM services in project `geminihub-486523`. Use the following UTC query windows on 2026-10-28:

| Observation | Query start, inclusive | Query end, inclusive | Limit |
| --- | --- | --- | ---: |
| Original | `08:40:00Z` | `08:52:00Z` | 500 |
| Rerun | `09:16:04Z` | `09:17:05Z` | 100 |

```sh
gcloud logging read 'resource.type="gce_instance" AND labels.service_name:"kakeratta" AND jsonPayload.path="model-usage" AND timestamp>="2026-10-28T09:16:04Z" AND timestamp<="2026-10-28T09:17:05Z" AND (jsonPayload.wake="daily" OR jsonPayload.purpose="daily_summary")' --project=geminihub-486523 --limit=100 --format=json
```

Check workspace attribution if other workspaces run in the same window. Deduplicate by `insertId`, group by `purpose` and `model`, and require input/output usage for every priced call. The cost formula is `(input_tokens * 0.30 + output_tokens * 0.90) / 1,000,000` USD. Reasoning and cached-token counts are breakdowns; do not add them again. These observations contain no schedule-planning or review calls; their model selection is covered by the regression test linked below.

The rerun was a one-time operator action requested by the owner. The nine personas' completed-day markers and the shared daily marker were backed up, then moved back one day together with their previous-run timestamps, using update-time preconditions. One authenticated Cloud Tasks wake used the existing production due queue. Afterward all ten markers were verified as claimed for the current day again. Existing posts and memory were retained, and normal once-per-day behavior resumed. This did not add a public rerun API or a recurring extra wake.

Costs are estimates from observed tokens, not provider invoices. Search, embeddings, infrastructure, ordinary post replies and external-agent inference are outside this comparison. JPY figures use a fixed illustrative exchange rate rather than the day's market rate; taxes, discounts and credits are not applied. The two runs are both chargeable work, so the lower rerun cost does not replace the first run's cost. One day's rerun does not establish a monthly average.

Implementation: [daily.go](../../internal/persona/daily.go), [runner.go](../../internal/persona/runner.go), and [workflow.go](../../internal/persona/workflow.go). Regression verification: `TestNebiusDailyWorkUsesSuper` in [daily_test.go](../../internal/persona/daily_test.go), including schedule planning/review and an AlwaysUltra persona. See [Models on Nebius](../system/personas.md#models-on-nebius) for the current selection rules.
