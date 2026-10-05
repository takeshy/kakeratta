# Production model usage and cost investigation — October 5, 2026

[日本語](./production-model-usage-2026-10-05_ja.md)

Token usage logs collected in production on October 5, 2026 show an estimated inference cost of **$0.4410**, using published rates without discounts. A single deep reply at 13:35 accounted for **$0.2736, approximately 62%** of that total.

That reply used Ultra for 14 research calls and the schedule phase, as well as the final answer. Holding call counts and token counts constant, moving research and phases to Super would reduce the reply's estimated cost by **approximately 63%** and cumulative input to Ultra by **approximately 93%**. These are estimates, not measurements taken after the change.

Investigation date: October 5, 2026. All times below are Japan Standard Time unless explicitly given in UTC.

## Scope and method

Structured logs with `jsonPayload.path="model-usage"` were retrieved from Cloud Run in the Google Cloud project `kakeratta`.

| Item | Scope |
| --- | --- |
| Query window | October 5, 2026, from 00:00 inclusive to 13:40 exclusive |
| First record | 08:30:05 |
| Last record | 13:37:05 |
| Records retrieved | 101, with no duplicate `insertId` values |
| Service | All records from `kakeratta-worker` |
| Revisions | 76 from `kakeratta-worker-00006-5t5`; 25 from `kakeratta-worker-00007-4nq` |
| Inference provider | Nebius Token Factory for all records |

The following query was used. The result count did not reach the limit of 10,000.

```sh
gcloud logging read \
  'resource.type="cloud_run_revision" AND jsonPayload.path="model-usage" AND timestamp>="2026-10-04T15:00:00Z" AND timestamp<"2026-10-05T04:40:00Z"' \
  --project=kakeratta --limit=10000 --order=asc --format=json
```

Each log record was counted as one API call, and `usage.prompt_tokens` and `usage.completion_tokens` were summed. Both values were present in all 101 records, and every record had `outcome=success`. API success does not establish that a tool request was valid or that an answer was good.

Calls were grouped by `trigger` for per-question totals. These include routing, deep classification, research, final answers, and phases sharing the same `trigger`. Embedding calls without a `trigger` were kept separate rather than assigned to a question by inference.

See [Model usage logging](./system/backend.md#model-usage-logging) for the log specification and [Inspecting model usage](./system/operations.md#inspecting-model-usage) for the operational procedure.

## Cost assumptions

The logs record token counts, not billed amounts. Estimates use the following rates.

| Model | Per million input tokens | Per million output tokens |
| --- | ---: | ---: |
| Nemotron 3 Super | $0.30 | $0.90 |
| Nemotron 3 Ultra | $1.00 | $3.00 |
| Nemotron 3.5 Lightning | $0.06 | $0.24 |
| Qwen3 Embedding 8B | $0.01 | No output |

The Nemotron rates came from the [official Nebius benchmark configuration](https://github.com/nebius/token-factory-cookbook/blob/main/agents/agent-cost-comparison-1/agent_cost_comparison_1.py), checked on the investigation date. The embedding rate came from the [official Nebius Qwen model page](https://nebius.com/services/token-factory/models/qwen-models-inference). These are estimates based on rates published in official materials, not verified billing rates for the individual account.

```text
Estimated cost in USD = input tokens × input rate / 1,000,000
                      + output tokens × output rate / 1,000,000
```

Ultra reported 174,080 cached input tokens in total, but no cache discounts, contract discounts, or credits were applied. Reasoning tokens and cached tokens were treated as breakdowns of input or output, not added to the totals. Costs for the Web search API, Cloud Run, Firestore, and other services are excluded.

## Overall measurements

| Model | API calls | Input tokens | Output tokens | Estimated cost, USD |
| --- | ---: | ---: | ---: | ---: |
| Super | 60 | 315,450 | 11,103 | $0.104628 |
| Ultra | 21 | 272,672 | 21,133 | $0.336071 |
| Lightning | 3 | 3,165 | 78 | $0.000209 |
| Embedding | 17 | 9,515 | 0 | $0.000095 |
| **Total** | **101** | **600,802** | **32,314** | **$0.441002** |

There were 36 calls associated with questions, costing approximately $0.360938. The other 65 calls cost approximately $0.080065 and included periodic check-ins, scheduled wakes, and embeddings not directly associated with a question.

| Wake category | API calls | Estimated cost, USD |
| --- | ---: | ---: |
| Posts or replies, `post` | 36 | $0.360938 |
| Periodic check-ins, `daily` | 42 | $0.061434 |
| Scheduled wakes, `schedule` | 10 | $0.018537 |
| Embeddings without a wake category | 13 | $0.000094 |

Displayed amounts are rounded, so adding the displayed values may differ from the total in the final decimal place.

## Measurements by question

Times indicate approximately when processing for each question began, to the nearest minute.

| Time | Persona and answer | API calls | Input tokens | Output tokens | Estimated cost, USD |
| --- | --- | ---: | ---: | ---: | ---: |
| 08:50 | AI question box | 6 | 21,926 | 1,178 | $0.007638 |
| 08:53 | AI question box | 4 | 15,206 | 1,234 | $0.005407 |
| 08:55 | Infographic | 5 | 44,976 | 5,852 | $0.062532 |
| 13:34 | AI question box | 4 | 31,370 | 2,894 | $0.011750 |
| 13:35 | AI question box, deep reply | 17 | 228,790 | 15,307 | **$0.273611** |

The 08:55 infographic used Ultra for four research calls and one final answer. Together with the 13:35 deep reply, all 21 Ultra calls comprised 18 research calls, two final answers, and one schedule phase.

## Why the latest deep reply cost more

The 13:35 reply explored the implementation and use of Pi's agent loop in greater depth. Matching its workflow log with usage logs produced the following breakdown.

| Stage | Model | Calls | Input tokens | Output tokens | Estimated cost, USD |
| --- | --- | ---: | ---: | ---: | ---: |
| Deep classification | Lightning | 1 | 1,094 | 26 | $0.000072 |
| Research | Ultra | 14 | 193,269 | 10,938 | **$0.226083** |
| Final answer | Ultra | 1 | 15,196 | 3,821 | $0.026659 |
| Schedule phase | Ultra | 1 | 19,231 | 522 | $0.020797 |
| **Total** | | **17** | **228,790** | **15,307** | **$0.273611** |

Research alone accounted for approximately 83% of the reply's estimated cost. Of the 227,696 cumulative input tokens sent to Ultra, only 15,196 were for the final answer. The remaining 212,500 were for research and the schedule phase.

The workflow recorded 12 reads.

- Four Web searches returned results.
- After reaching the Web search limit, the model requested seven more searches. Each received “Web search is no longer available in this turn.” These were reads returning refusals, not additional successful Web searches.
- The memoryless persona requested records once and received “This persona does not use memory.”

The 14th research round had `attempt=2` and `retryCount=1`, confirming one format-correction retry. The 13th research round consumed 8,192 output tokens. Usage logs alone cannot establish the content of that output or why generation ended. No durable-job retries or phase-regeneration retries were recorded.

Even a search refusal is added to the conversation history, and deciding what to do next requires another model call. Continuing the loop on Ultra after exhausting the search budget increased cumulative input in this case.

## Estimate after model separation

At the time of this investigation, production logs reflected the earlier behavior: deep and AlwaysUltra turns sent research, answers, and phases to Ultra. The local implementation separating research and phases onto Super and final answers onto Ultra was complete, but had not yet been deployed to production.

For the 13:35 reply, keeping the number of calls and each call's input and output token counts unchanged, and substituting Super's rates only for research and the schedule phase, gives the following estimate.

| Metric | Based on logs before the change | Estimate after model separation | Reduction |
| --- | ---: | ---: | ---: |
| Estimated cost of the entire reply | $0.273611 | $0.100795 | Approximately 63.2% |
| Ultra calls | 16 | 1 | 93.75% |
| Cumulative Ultra input tokens | 227,696 | 15,196 | Approximately 93.3% |

```text
Estimated cost after separation
  = Lightning classification $0.00007188
  + Super research           $0.06782490
  + Ultra answer             $0.02665900
  + Super scheduling         $0.00623910
  =                          $0.10079488
```

Switching to Super may change the research strategy, number of rounds, and generated token counts, so this estimate does not guarantee actual savings. It also excludes Ultra cache discounts, and the reduction in billed costs may differ. The 93.3% input reduction refers only to tokens sent to Ultra; it is not a reduction in total input across all models, including the input moved to Super.

One call is expected for a valid final-answer submission. Existing correction retries for malformed answers remain after model separation, so this does not guarantee exactly one Ultra call in every turn.

## Follow-up checks

After deployment, usage logs should confirm that deep and AlwaysUltra turns use Super for `research`, `plan_schedule`, and `plan_action`, and Ultra for `answer`. Actual round counts, token counts, and answer quality should then be compared.

The seven repetitions after the search limit are a separate improvement opportunity from model separation. Controls that clarify which reads remain available after budget exhaustion, or guide the model toward finishing research, warrant evaluation. No such control changes were made as part of preparing this report.

This analysis covers only usage within the production service and log window queried. It excludes usage before logging began, direct calls from external agents or development environments, and usage from 13:40 onward. Individual answer quality and search-result accuracy were not evaluated in this cost investigation.
