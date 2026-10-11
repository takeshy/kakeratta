# Recent production AI cost — October 11, 2026

The Settings cost reference now uses four complete JST days, **October 7–10, 2026**. The recorded AI calls total **US$0.67668243**, or **US$0.1691706075 per day**. Repeating this workload for 30 days would yield **US$5.075118225**; the UI shows the observed daily range as US$0.03–0.46 and rounds the 30-day extrapolation to about US$5.

## Scope and evidence

- Retrieved October 11, 2026, from the shared-VM production project `geminihub-486523`.
- 234 records with distinct insert IDs, covering the production web and worker services. The 20,000-record query limit was not reached.
- All records are Nebius calls with reported input and output token counts and known model rates. 0 records have a non-success outcome.
- Query covers midnight October 7 through midnight October 11 JST; it excludes the incomplete current day.
- Private raw logs stay in ignored `tmp/recent-production-model-usage-four-days.json`; no prompts, entry IDs or persona identities are included here.

```sh
gcloud logging read 'resource.type="gce_instance" AND labels.service_name:"kakeratta" AND timestamp>="2026-10-06T15:00:00Z" AND timestamp<"2026-10-10T15:00:00Z" AND jsonPayload.path="model-usage"' --project=geminihub-486523 --limit=20000 --order=asc --format=json
```

## Rates and limitations

Rates match [internal/usage/usage.go](../internal/usage/usage.go), checked against the official [Nemotron](https://nebius.com/services/token-factory/models/nvidia-nemotron-models-inference) and [Qwen](https://nebius.com/services/token-factory/models/qwen-models-inference) pages on October 11: Super US$0.30/0.90 and Ultra US$1.00/3.00 per million input/output tokens; Embedding US$0.01 per million input tokens.

Costs are public-rate estimates from reported token counts, not reconciled invoices. Reasoning and cache counts are breakdowns and are not added again. Discounts, credits, search, infrastructure, development calls outside these services, and direct external-agent charges are excluded. The observation window and workload differ from October 1–4; this is a new usage example, not a controlled measurement of a saving percentage. The two most recent days alone cost US$0.06113692, which extrapolates to only US$0.92 over 30 days. Including October 7–8 captures heavier use and produces the broader four-day reference. October 7 includes work before some of the recent optimizations; this is a recent production example spanning those changes, not an exclusively post-change benchmark.

## Daily totals (JST)

| Date | Calls | Input tokens | Output tokens | Estimated USD |
| --- | ---: | ---: | ---: | ---: |
| 2026-10-07 | 145 | 625,602 | 98,335 | 0.45960127 |
| 2026-10-08 | 52 | 314,706 | 18,813 | 0.15594424 |
| 2026-10-09 | 16 | 82,978 | 4,385 | 0.02881960 |
| 2026-10-10 | 21 | 79,360 | 4,521 | 0.03231732 |

## Models

| Model | Calls | Input tokens | Output tokens | Estimated USD |
| --- | ---: | ---: | ---: | ---: |
| Qwen/Qwen3-Embedding-8B | 56 | 20,203 | 0 | 0.00020203 |
| nvidia/Nemotron-3-Ultra-550b-a55b | 10 | 112,430 | 75,999 | 0.34042700 |
| nvidia/nemotron-3-super-120b-a12b | 168 | 970,013 | 50,055 | 0.33605340 |

## Purposes

| Purpose | Calls | Input tokens | Output tokens | Estimated USD |
| --- | ---: | ---: | ---: | ---: |
| answer | 70 | 485,091 | 87,019 | 0.45766750 |
| daily_summary | 2 | 4,912 | 5,787 | 0.00668190 |
| embedding | 56 | 20,203 | 0 | 0.00020203 |
| memory_summary | 4 | 10,066 | 1,117 | 0.00402510 |
| memory_update | 8 | 30,847 | 2,956 | 0.01191450 |
| monthly_summary | 2 | 52,078 | 14,625 | 0.02878590 |
| persona_builder | 1 | 3,166 | 1,076 | 0.00639400 |
| plan_schedule | 26 | 112,313 | 2,973 | 0.03636960 |
| research | 48 | 351,224 | 5,739 | 0.11053230 |
| review | 1 | 2,782 | 236 | 0.00104700 |
| routing | 16 | 29,964 | 4,526 | 0.01306260 |
