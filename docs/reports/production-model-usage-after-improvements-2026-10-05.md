# Model usage after improvements — October 5, 2026

[日本語](./production-model-usage-after-improvements-2026-10-05_ja.md)

**Production confirms the model separation.** All 12 research calls and six schedule-planning calls used Super. All three Ultra calls were final answers. Repricing the new deep reply's actual token counts under the previous model assignment yields $0.051536 versus the observed assignment's estimated $0.026771: a 48.1% difference attributable to model rates alone.

Two infographic answers account for 54.8% of the observed total. No observed turn exhausted its Web search budget, so this sample does not establish production behavior after budget exhaustion.

## Scope and evidence

Compare with the [before-change report](./production-model-usage-2026-10-05.md). Times are JST unless stated otherwise.

- Improvement commit: `e98a457`, separating research/answer models and enforcing live read capabilities.
- Local HEAD during investigation: `d162ed0`; `git pull --ff-only` returned Already up to date.
- Snapshot retrieved around 21:09 on October 5, covering logs from 13:40 onward.
- 64 retrieved records: 54 model-usage, eight workflow and two routing records. The 10,000-record query limit was not reached.
- All 54 usage records have distinct `insertId` values, Nebius as provider, successful API outcomes and both input/output token counts.
- First usage: 17:00:17; last usage: 19:32:37.
- All usage came from `kakeratta-worker-00010-cwq`, created at 16:36:42. Revisions 00008 and 00009 were created at 14:35:12 and 16:20:27 but have no usage records in this snapshot.
- Deployed image digest: `sha256:06c9900810febdbe571b1e2bfe04380f10e9c5080c51bd834d72882576eb8515`. An exact image-to-Git-SHA mapping was not established; the changed model assignment was verified directly from production calls.

```sh
gcloud logging read \
  'resource.type="cloud_run_revision" AND (jsonPayload.path="model-usage" OR jsonPayload.path="workflow" OR jsonPayload.message:"routing") AND timestamp>="2026-10-05T04:40:00Z"' \
  --project=kakeratta --limit=10000 --order=asc --format=json
```

The analysis uses the retrieved snapshot; rerunning this open-ended query adds later records. Private raw data is stored locally at `/tmp/kakeratta-model-usage-after-2026-10-05.json` and the earlier workflows at `/tmp/kakeratta-model-usage-before-workflows-2026-10-05.json`. Prompt/answer/tool contents are not committed.

## Rates and totals

Use the same rates as the baseline: Super $0.30/$0.90 input/output per million tokens, Ultra $1.00/$3.00, Lightning $0.06/$0.24, and Embedding $0.01 input. Official [Nemotron rates](https://nebius.com/services/token-factory/models/nvidia-nemotron-models-inference) and [Qwen rates](https://nebius.com/services/token-factory/models/qwen-models-inference) were also checked during this investigation.

Costs are estimates from reported tokens, not reconciled bills. Cache/contract discounts, credits, search/server costs and external-agent direct calls are excluded. Reasoning/cache counts are breakdowns, not additional tokens. All three new Ultra calls reported zero cached input tokens.

| Model | Calls | Input tokens | Output tokens | Estimated USD |
| --- | ---: | ---: | ---: | ---: |
| Super | 25 | 161,629 | 5,156 | 0.053129 |
| Ultra | 3 | 32,268 | 17,520 | 0.084828 |
| Lightning | 4 | 4,368 | 102 | 0.000287 |
| Embedding | 22 | 22,768 | 0 | 0.000228 |
| **Total** | **54** | **221,033** | **22,778** | **0.138471** |

| Purpose | Calls | Estimated USD |
| --- | ---: | ---: |
| Research | 12 | 0.027199 |
| Final answer | 8 | 0.096498 |
| Schedule planning | 6 | 0.012624 |
| Routing | 2 | 0.001636 |
| Deep classification | 4 | 0.000287 |
| Embedding | 22 | 0.000228 |

Post/reply calls total 29 and $0.130197; scheduled wakes total three and $0.008047; embeddings without a wake category total 22 and $0.000228. There are no usage records for daily check-ins, memory updates, action planning or review in this window.

The baseline total of $0.441002 and new total of $0.138471 are different workloads, not a measured saving rate. The baseline included 42 daily-check-in calls, and the questions, diagrams and observation times differ.

## Post and reply results

Start times are the minute of the first recorded call. Seven posts/replies received answers. A separate 19:11 entry had only one routing call ($0.000808).

| Start | Answer | Calls | Input | Output | Estimated USD |
| --- | --- | ---: | ---: | ---: | ---: |
| 19:16 | Ordinary | 5 | 18,378 | 1,061 | 0.006468 |
| 19:17 | Ordinary | 4 | 15,780 | 1,720 | 0.006037 |
| 19:19 | Ordinary | 4 | 19,035 | 424 | 0.005594 |
| 19:20 | Ordinary | 5 | 27,582 | 528 | 0.008573 |
| 19:22 | Deep reply | 6 | 43,827 | 2,826 | 0.026771 |
| 19:27 | Infographic | 2 | 18,015 | 7,740 | 0.034818 |
| 19:32 | Infographic | 2 | 29,411 | 7,386 | 0.041128 |

All eight workflows, including one scheduled wake, have `ran=true`, a recorded publication and no error. This verifies execution/publication, not answer accuracy or quality. There are no recorded correction, durable-job, note-job or phase-regeneration retries.

## Separating the deep reply's improvements

| Stage | Model | Calls | Input | Output | Estimated USD |
| --- | --- | ---: | ---: | ---: | ---: |
| Deep classification | Lightning | 1 | 748 | 24 | 0.000051 |
| Research | Super | 3 | 23,615 | 227 | 0.007289 |
| Final answer | Ultra | 1 | 8,628 | 2,493 | 0.016107 |
| Schedule planning | Super | 1 | 10,836 | 82 | 0.003325 |
| **Total** | | **6** | **43,827** | **2,826** | **0.026771** |

The previous 13:35 deep reply used 17 calls and cost an estimated $0.273611, including 14 Ultra research calls. The new reply used six calls and cost $0.026771, including three Super research calls. The observed 90.2% cost difference is **not a same-question saving measurement**, because the questions differ.

Holding the new calls and token counts fixed, assigning research and schedule planning to Ultra would cost $0.051536. The observed assignment costs $0.026771, a $0.024765 or 48.1% difference. This is a rate-only counterfactual, not an Ultra rerun.

Across the three Ultra-answer turns including diagrams, the corresponding totals are $0.144340 under the old model assignment and $0.102717 under the observed assignment, a 28.8% difference. The baseline's predicted 63% reduction applied to its much longer research case; these different reductions are consistent with different stage proportions.

## Read-control evidence

The new workflows contain four reads, all Web searches: one in each of two ordinary turns and two in the deep turn. There are no outputs matching the earlier “Web search is no longer available in this turn.” or “This persona does not use memory.” refusals. Both diagrams finished research in one call without executing reads.

No ordinary turn reached its two-search budget, and the deep turn did not reach its four-search budget. Absence of repeated refusals is a favorable observation, but not a production test after exhaustion.

Existing tests `TestTurnModelsSeparateResearchFromAnswer`, `TestResearchSearchCapabilitiesFollowExecutedBudget`, `TestReadCapabilitiesKeepMemoryAndEntryReadsAfterLookupExhaustion`, and `TestReadAvailability*` were rerun successfully in persona/tools. Test evidence is separate from production answer quality and budget-exhaustion evidence.

## Remaining opportunities

1. **Infographic output size.** Two diagrams cost $0.075946, 54.8% of the total. Their Ultra outputs were 7,699 and 7,328 tokens, with only 152 and 131 reasoning tokens. Of 15,027 output tokens, 14,744 (98.1%) were non-reasoning. Inspect HTML size, repeated CSS and information density; lowering reasoning alone is unlikely to save much on these diagrams. A lower output limit can truncate HTML, so completeness and quality need validation.
2. **Empty schedule plans.** Six schedule calls all produced zero operations, costing $0.012624 (9.1% of the total). They also verify promises about schedules/actions, so empty output does not prove the calls were unnecessary. Investigate safe conditions for skipping planning when no scheduled work is needed.
3. **Large answer inputs.** The second diagram's final-answer input was 14,669 tokens versus 8,971 for the first. Measure target content, thread history and research-result contributions before trimming needed context.
4. **A controlled exhausted-budget comparison.** Replay the same inputs, persona configuration and tool results to compare rounds, refusals, retries, cost and answer quality. No additional paid inference or production setting changes were made for this investigation.

This investigation changed no implementation, model settings, schedules or memory.
