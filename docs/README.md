# Documentation

[Project README](../README.md) · [プロジェクト README（日本語）](../README_ja.md)

Start with the guides for setup and everyday use. The system bundle documents the current implementation; design notes explain the background and decisions. Reports preserve dated experiments and production observations, so their settings and conclusions may describe an earlier version.

English and Japanese editions stay together, with `_ja.md` identifying the Japanese counterpart when both are available.

## Directory map

| Directory | Contents |
| --- | --- |
| [guides/](./guides/) | Usage, integrations, and reviewer account setup |
| [design/](./design/) | Project background, technical design, and implementation lessons |
| [system/](./system/index.md) | Current implementation in the English OKF v0.2 subsystem bundle |
| [reports/](./reports/) | Model comparisons, integration verification, costs, and incident reports |
| [captures/](./captures/) | Japanese and English screenshots used by the READMEs and public site |
| [hackathon/](./hackathon/) | Submission text, thumbnails, gallery, and demo materials |

## Guides

| Topic | Document | Japanese edition |
| --- | --- | --- |
| Usage and configuration | [Guide](./guides/usage.md) | [使い方と設定](./guides/usage_ja.md) |
| External agents and Hermes | [Guide](./guides/hermes.md) | [外部エージェント連携](./guides/hermes_ja.md) |
| Personal Google reads through Hermes | [接続と運用手順（日本語）](./guides/google_ja.md) | — |
| Bluesky sharing | [Guide](./guides/social-sharing.md) | [会話をBlueskyに公開](./guides/social-sharing_ja.md) |
| Alexa setup | [接続手順（日本語）](./guides/alexa.md) | — |
| Judge and Alexa reviewer access | [Account setup](./guides/judge-access.md) | — |
| Judge sample workspace | [Sample personas and data](./guides/judge-sample-data.md) | — |

For deployment, see the infrastructure guide: [English](../infra/README.md) / [日本語](../infra/README_ja.md). For illustrated everyday instructions, see the public manual: [English](https://kakeratta.net/manual/?lang=en) / [日本語](https://kakeratta.net/manual/?lang=ja).

## Design and implementation

| Topic | Document | Japanese edition |
| --- | --- | --- |
| Project background | [Why kakeratta exists](./design/background.md) | [作った理由](./design/background_ja.md) |
| Technical design | [Under the hood](./design/technical.md) | [中で起きていること](./design/technical_ja.md) |
| AI implementation lessons | [What was hard about the AI](./design/ai-lessons.md) | [AIまわりで苦労したこと](./design/ai-lessons_ja.md) |
| Direct-request worker | [経緯と仕様（日本語）](./design/direct-request-worker-spec_ja.md) | — |
| Personal Google Bridge | [独立Webアプリの調査と実装設計（日本語）](./design/google-hermes-integration-spec_ja.md) | — |

The [system bundle index](./system/index.md) links to architecture, backend APIs and persistence, persona execution and memory, frontend behavior, integrations, and development operations.

## Reports

| Topic | Report | Japanese edition |
| --- | --- | --- |
| NemoClaw / Hermes integration and verification | [Integration report](./reports/nemoclaw-hermes-integration.md) | [統合・検証レポート](./reports/nemoclaw-hermes-integration_ja.md) |
| NemoClaw / Hermes model comparison | [Comparison](./reports/nemoclaw-hermes-model-comparison.md) | — |
| Hermes runaway-loop incident | [Incident report](./reports/nemoclaw-hermes-runaway-incident.md) | — |
| Hermes-4-405B visual evaluation, 2026-10-14 | [Evaluation report](./reports/hermes-4-405b-visual-evaluation.md) | [実測レポート](./reports/hermes-4-405b-visual-evaluation_ja.md) |
| Routing cost experiment, 2026-09-17 | [Experiment](./reports/routing-cost-experiment.md) | — |
| Routing model comparison, 2026-09-19 | [Comparison](./reports/routing-model-comparison-2026-09-19.md) | [振り分けモデル比較](./reports/routing-model-comparison-2026-09-19_ja.md) |
| Switchyard comparison, 2026-10-03 | [Comparison](./reports/switchyard-comparison-2026-10-03.md) | [Switchyard比較試験](./reports/switchyard-comparison-2026-10-03_ja.md) |
| Production model usage, 2026-10-05 | [Usage report](./reports/production-model-usage-2026-10-05.md) | [使用量と費用](./reports/production-model-usage-2026-10-05_ja.md) |
| Production usage after improvements, 2026-10-05 | [Follow-up](./reports/production-model-usage-after-improvements-2026-10-05.md) | [改善後分析](./reports/production-model-usage-after-improvements-2026-10-05_ja.md) |
| Production AI costs, 2026-10-07 | [Cost report](./reports/production-ai-cost-2026-10-07.md) | [費用レポート](./reports/production-ai-cost-2026-10-07_ja.md) |
| Production AI costs, 2026-10-11 | [Cost snapshot](./reports/production-ai-cost-2026-10-11.md) | — |

## Adding or moving documentation

Place new documents in the directory matching their purpose and add them to this index. Keep language pairs together. When moving a document, update incoming links, its own relative links to source files and screenshots, and GitHub URLs pointing at the current branch. Keep URLs pinned to historical commits unchanged. Preserve the subsystem chapter structure and OKF format under `system/`.
