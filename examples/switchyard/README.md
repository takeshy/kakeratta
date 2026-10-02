# Switchyard comparison

kakeratta remains a Go application. An optional separate Switchyard server selects
Lightning, Super, or Ultra over OpenAI Chat Completions. The default `ROUTER_MODE=lightning`
keeps the existing behavior. No Rust library or FFI is linked into kakeratta.

## Start the router

This configuration follows the Switchyard v0.3.0 native TOML schema. Install that
version with a native Rust toolchain:

```sh
cargo install --locked switchyard-server --version 0.3.0
export TOKEN_FACTORY_API_KEY='your-token-factory-key'
switchyard-server --config examples/switchyard/routes.toml --dry-run
switchyard-server --config examples/switchyard/routes.toml \
  --host 127.0.0.1 --port 4000 --routing-log-file /tmp/switchyard-routing.jsonl
```

Model IDs match kakeratta's Token Factory defaults; verify availability on your
account. The sample uses a custom three-group classifier, with Lightning as both judge
and the lightest answering destination:

| Group | Model | Intended requests |
| --- | --- | --- |
| `light` | Lightning | Pure thanks, greetings, acknowledgements, trivial questions |
| `standard` | Super | Ordinary questions, added conditions, straightforward research |
| `deep` | Ultra | Corrections, reconsideration, detailed comparison, complex reasoning |

The prompt distinguishes "ありがとう" from "ありがとう。駅から近い店も教えて"
and prioritizes the current reply over the difficulty of an earlier question.
These are classifier instructions, not guaranteed live verdicts.
`every_request` intentionally needs no session identity and includes tool
continuations; the judge sees recent conversation context. Invalid/unroutable
verdicts default to Ultra. In custom mode, judge HTTP failures stop the request
after retries, rather than selecting a fallback model. Eligible completion
failures try the remaining `models.any` targets in order; timeouts stop.
Custom mode requires the judge provider to support JSON Schema structured output
(the former two-tier `json_object` setting does not apply to custom mode).
The fixed `kakeratta-ultra` route preserves Always think deeply.

Sources: [routing overview](https://nvidia-nemo.github.io/Switchyard/routing_algorithms/overview/),
[TOML schema](https://nvidia-nemo.github.io/Switchyard/reference/toml_schema/),
[server CLI](https://nvidia-nemo.github.io/Switchyard/cli_reference/).

## Run kakeratta

```sh
ROUTER_MODE=switchyard \
SWITCHYARD_BASE_URL=http://127.0.0.1:4000/v1 \
SWITCHYARD_MODEL=kakeratta-auto \
SWITCHYARD_ULTRA_MODEL=kakeratta-ultra \
go run .
```

These URL/model values are defaults. `SWITCHYARD_API_KEY` is optional and is only
for a proxy that requires its own bearer token. A workspace's provider key is
never sent to Switchyard: its upstream key is held by the Switchyard process.
This is an operator-wide experimental policy for every workspace in that process.
The standalone server is described upstream as a demo for personal use; keep the
sample bound to localhost. kakeratta does not start or deploy it.

Only built-in research and answer generation go through Switchyard. Persona
delivery routing, image interpretation, later action/schedule planning, review,
and memory use their existing clients and models. External-agent answers bypass
it; built-in fallback uses it. Lightning's reply-escalation call is skipped in
Switchyard mode. Always think deeply and explicit internal deep wakes use the
fixed Ultra route. Automatic turns use the deeper research allowance because
the target can change per request. Their persisted model slot is `switchyard`,
not a claim that Lightning, Super, or Ultra answered. Proxy errors follow the existing turn
failure path and do not silently call another provider.

## Replay answer-stage inputs

```sh
export REPLAY_API_KEY='your-token-factory-key'
go run ./cmd/router-replay -input examples/switchyard/replay.jsonl \
  -mode both > /tmp/router-comparison.jsonl
```

Replace the sample cases with captured real-post answer-stage transcripts.
Each JSONL case has a unique `id`, `messages` (system/user conversation), optional
`previousAnswer` and `reply` for the baseline's actual Lightning escalation,
`alwaysUltra`, and optional `expectedContains`. Both modes receive the same
transcript and the runtime `submit_answer` schema, validation, and repair loop.
Inputs must contain the context you want assessed, including completed research.
Replay runs no tools and writes no workspace state. Timeline citation IDs are
rejected because no timeline search was executed. This measures answer generation
and baseline escalation; it does not reproduce end-to-end research, action,
review, memory, or external-agent latency/cost. The shipped examples are synthetic,
not measured production evidence.

`-mode lightning` or `-mode switchyard` selects one arm; `both` alternates arm
order by case. Set `REPLAY_BASE_URL`, `REPLAY_LIGHTNING_MODEL`,
`REPLAY_SUPER_MODEL`, and `REPLAY_ULTRA_MODEL` to override the baseline.
The Switchyard variables above also apply. Entire inputs are validated before
any API calls; completion errors are recorded and cause a nonzero exit code.

Each output row includes the answer, wall-clock seconds, completion calls with
returned model ID/token usage/status, missing expected strings, and optional USD
estimate. `expectedContains` is a deterministic smoke check, not a quality score.
Blind-review paired answers for accuracy, context use, instruction following,
and usefulness; aggregate median/p95 latency, failures, and human quality scores.

Supply `-prices prices.json` to estimate visible completion cost, with values in
USD per million tokens, e.g. `{"your-model-id":{"input":0.1,"output":0.4}}`.
Use measured/current account prices, not the illustrative numbers above. Unknown
model prices or missing usage yield `estimatedVisibleUSD: null`. Include the
Lightning escalation call in baseline totals. Switchyard's internal judge calls
are not necessarily in API usage: reconcile its routing log and provider billing
before comparing total cost. Do not interpret the visible estimate as total cost.
Output files contain the generated answers and should be handled as workspace
data. No live evaluation is run by ordinary Go tests.
