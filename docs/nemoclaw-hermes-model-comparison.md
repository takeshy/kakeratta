# NemoClaw × Hermes Agent Model Notes: Nemotron-3-Super, Hermes-4-405B, Nemotron-3-Ultra

Date: 2026-09-28 (JST)
Environment: NemoClaw OpenShell sandbox (`hermes-sandbox`)
Inference: Nebius Token Factory (`api.tokenfactory.nebius.com/v1`) via the OpenShell L7 proxy (`https://inference.local/v1`)
Integration: kakeratta MCP (`list_asks`, `claim_ask`, `read_for_ask`, `submit_answer`)

[Integration report](nemoclaw-hermes-integration.md) · [Runaway loop incident](nemoclaw-hermes-runaway-incident.md)

This report records what was observed with three models used as the planner for Hermes Agent, and why the sandbox now runs `nvidia/Nemotron-3-Ultra-550b-a55b`. It is not a controlled benchmark: each observation below comes from a different prompt, and no model was given the same task as another.

## Models

| Model | Architecture | Input / 1M tokens | Output / 1M tokens |
| :--- | :--- | ---: | ---: |
| `nvidia/nemotron-3-super-120b-a12b` | MoE, 120B total / 12B active | $0.30 | $0.90 |
| `NousResearch/Hermes-4-405B` | Dense, 405B | $1.00 | $3.00 |
| `nvidia/Nemotron-3-Ultra-550b-a55b` | MoE, 550B total / 55B active | $1.00 | $3.00 |

Prices are from the Token Factory price list (`eu-north1`, non-batch; effective 2025-11-01 for 405B, 2026-03-01 for Super, 2026-06-01 for Ultra). The price list shows no separate rate for cached input.

## Observations

### Nemotron-3-Super: rationalizing an earlier error

User prompt:

> nemoclawとopenclawは別だよ？渡してくれたopenclawにちゃんとopenclawの説明があるよ
> ("NemoClaw and OpenClaw are different, right? The OpenClaw material you gave me has a proper explanation of OpenClaw.")

Reasoning (`messages.reasoning_content`):

```text
We need to answer: Are Nemoclaw and OpenClaw different? Explain that OpenClaw is the technology/base,
NemoClaw is a specific instance/brand of that technology... So they are not separate entities;
OpenClaw refers to the framework, NemoClaw is the implementation.
```

An earlier turn had wrongly said the two were the same. When the user corrected it, Super kept the earlier claim and invented a hierarchy to reconcile it, without looking anything up.

This is one case. Super also wrote a well-researched answer in another run and then failed to submit it (see the [integration report](nemoclaw-hermes-integration.md#key-operational-issues-resolved)).

### Hermes-4-405B: searching before answering

User prompt:

> hermesとopenclawの違いは？
> ("What is the difference between Hermes and OpenClaw?")

`agent.log`:

```text
2026-09-27 07:55:04 INFO tools.web_tools: Web search via tavily: 'hermes openclaw' (limit: 5)
2026-09-27 07:55:06 INFO agent.tool_executor: tool web_search completed (1.73s, 7713 chars)
2026-09-27 07:55:24 INFO agent.conversation_loop: API call #7: model=NousResearch/Hermes-4-405B in=25393 out=725 latency=17.6s cache=93%
```

405B ran a web search and answered with a five-point comparison. This is a different, uncorrected question, so it does not show how 405B handles a correction. The 17.6 s is the latency of one API call, not of the whole turn.

The same model, woken without usable tools, later ran 60-turn self-debugging loops that cost $34.98 in total ([incident report](nemoclaw-hermes-runaway-incident.md)).

### Nemotron-3-Ultra: connectivity check only

```bash
nemoclaw hermes-sandbox exec -- hermes -z "こんにちは！モデル名と特徴を1行で教えてください"
```

```text
nvidia/Nemotron-3-Ultra-550b-a55b（NVIDIA製・550BパラメータのNemotron 3 Ultra）をHermes Agent経由で実行中、プロバイダはcustom（NemoClaw経由のcompatible-endpoint）
```

This confirms only that the route to Ultra works.

### Nemotron-3-Ultra: the same topic, answered as two separate frameworks

After the switch, Ultra was asked about the difference between OpenClaw and Hermes Agent, the topic Super had confused. Super got it wrong in its first answer and again after the user corrected it. Ultra ran a web search and got it right the first time: it treated them as two separate self-hosted, open-source agent frameworks and compared them by design (OpenClaw as an orchestrator where developers register skills, plugins and sub-agents explicitly; Hermes Agent as a runtime that extracts reusable skills from completed tasks) and by the use cases each suits.

Because Ultra was right from the start, there was no error to correct, so this case does not show how Ultra responds to a correction. The answer also contained specific figures, such as release versions, access counts and GitHub stars, which were not checked for this report. Latency and token use were not recorded.

## Why Ultra

- **Super was ruled out** because it held on to its error: wrong in the first answer and still wrong after the user corrected it. A planner that cannot take a correction is not usable for kakeratta's personas.
- **405B was the first candidate** because it is the model Hermes Agent's own documentation recommends. It was dropped after the runaway incident.
- **Ultra was chosen** because kakeratta already uses it elsewhere: for persona drafting and deeper follow-up answers, and in the [routing comparison](routing-model-comparison-2026-09-19.md) it matched the baseline on 19 of 20 posts.

Ultra is not cheaper than 405B: both are priced at $1.00 per million input tokens and $3.00 per million output tokens, about 3.3 times Super. Had the runaway loop run on Ultra, the bill would have been about the same. What prevents a repeat is the fail-closed Wake Gate and the 15-turn limit, not the model choice.

Beyond the single answer above, Ultra's behavior as the Hermes planner has not yet been measured against the other two models. A fair comparison would give all three the same asks, including a correction like the Super case above, and record turns, tokens, and latency per ask.

## Switching the model

```bash
# 1. Point the OpenShell inference route at the new model
nemoclaw inference set --provider compatible-endpoint --model "nvidia/Nemotron-3-Ultra-550b-a55b" --sandbox hermes-sandbox

# 2. Find the main model setting in config.yaml
nemoclaw hermes-sandbox exec -- grep -n "model" /sandbox/.hermes/config.yaml
```

Edit only the main model entry found in step 2. `config.yaml` can contain other `model:` keys (for example auxiliary models), so do not replace every match.

```bash
# 3. Find the kakeratta cron job's pinned model
nemoclaw hermes-sandbox exec -- grep -n '"model"' /sandbox/.hermes/cron/jobs.json
```

Update the `model` of the kakeratta job only. Hermes pins the model per job and suspends a job whose model no longer matches the global configuration.

```bash
# 4. Check that Hermes answers
nemoclaw hermes-sandbox exec -- hermes -z "Hello, verify model ID"
```
