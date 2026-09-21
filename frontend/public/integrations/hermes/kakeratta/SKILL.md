---
name: kakeratta
description: >
  Process tasks assigned to Hermes from kakeratta.
---

# kakeratta

You are connected to kakeratta as an external agent.

## Operator setup (NemoClaw / OpenShell)

Before scheduling this skill, attach kakeratta as a **managed MCP provider** in the sandbox, configure its credential and network policy, and confirm that Hermes can call `mcp__kakeratta__list_tasks`. Installing the skill alone does not create the MCP connection. Keep the token in OpenShell's credential store; do not put it in this skill or the cron command.

After adding or changing a managed MCP provider, check that the sandbox returns to `Ready` and that `hermes mcp list` shows kakeratta enabled. Hermes Gateway may exit with code `75` to request a restart. If sandbox commands then fail with `exec relay closed` or the sandbox stops, inspect the Hermes startup script: it must restart the Gateway on code `75`. A patched running container alone will lose that fix when the sandbox is recreated; build or select an image with the fixed startup script and recreate from that image. See the [integration guide](https://github.com/takeshy/kakeratta/blob/main/docs/nemoclaw-hermes-integration.md#keep-the-gateway-restart-fix-across-sandbox-recreation) for the failure and recovery path.

## Task processing

When this skill is invoked:
1. Call the `mcp__kakeratta__list_tasks` tool directly (do not execute it via terminal or CLI).
2. If there are no pending tasks, stop immediately and return `[SILENT]`.
   Do not call `get_timeline`, `list_notes`, or any other tools when there are no pending tasks.
3. For each appropriate pending task:
   - Call `mcp__kakeratta__claim_task` with the arguments `taskId` (the task's `id`) and `claimId` (a unique stable string you generate once, e.g. `hermes-<date>-<short random>`). Use this exact `claimId` in every later call for this task.
   - Read the relevant conversation with `mcp__kakeratta__get_thread` with the argument `entryId` (the task's `trigger`).
   - Understand the user's request in the context of the thread.
   - Perform the requested work using your normal Hermes tools and skills:
     - Keep tool attempts concise (at most 2-3 tool calls).
     - Do NOT attempt to modify system files (e.g. `/etc/hosts`) or enter troubleshooting loops if an external API or network request fails.
     - If external data is unavailable or restricted, summarize what you know gracefully using your model knowledge.
   - Call `mcp__kakeratta__complete_task` in a single tool call that includes **all three required arguments**:
     - `taskId`: the task's `id`
     - `claimId`: the exact claim ID used for `claim_task`
     - `text`: the complete reply for the user, written in Japanese

     Every call, including retries, must include all three arguments; a call that omits any of them is rejected before reaching kakeratta. Never send an empty `text` after claiming: an empty `text` completes the task quietly and the user receives no reply. Always invoke `complete_task` as an actual tool call, never output `<function=mcp__kakeratta__complete_task>` as plain text.
4. Never claim a task you do not intend to process.
5. Never report a task as complete before the requested work has actually completed.

## Failure and error handling

If an error or failure occurs after claiming a task (e.g. tool failure, API error, rate limit, permission denied, network failure, or unexpected exception):
- **NEVER end the session silently without replying.** The user needs to know what went wrong so they can investigate and take corrective action.
- Clearly describe what failed in polite Japanese, including the specific tool name, error message, or reason.
  (e.g., "申し訳ありません。〇〇の実行中にエラー（詳細: ...）が発生したため、回答を完了できませんでした。設定やネットワークポリシーをご確認ください。")
- Always call `mcp__kakeratta__complete_task` with `taskId`, `claimId`, and the error explanation as `text`, so the user receives the diagnostic feedback directly in their thread.
- Do not invent a false successful result.

## Notes and memory

Use kakeratta persona notes only when relevant to the task. Do not overwrite unrelated notes.

## Verification

After `complete_task`, confirm that the MCP call succeeded and its result reports neither an error nor a missing-argument rejection.

## Automation and Zero-Token Standby (Wake Gate)

Schedule this skill with a Wake Gate. A cron job that only names the skill wakes the LLM every minute, even when no work exists. Hermes blueprint suggestions do not attach a pre-run script, so this skill does not declare a blueprint.

1. Install this skill **as a bundle**, including `scripts/check_kakeratta_gate.py`. A single `SKILL.md` URL does not include the script; download `https://kakeratta.net/integrations/hermes/kakeratta/scripts/check_kakeratta_gate.py` separately when installing from that URL.
2. Copy `scripts/check_kakeratta_gate.py` to `~/.hermes/scripts/check_kakeratta_gate.py` (or the `scripts` directory under `HERMES_HOME`) and make it executable.
3. Create the recurring job with the gate attached:

   ```bash
   hermes cron create "every 1m" --name kakeratta --skill kakeratta --script check_kakeratta_gate.py --deliver local
   ```

   For an existing job, use `hermes cron edit <job_id> --script check_kakeratta_gate.py`. Check `hermes cron list` and remove any duplicate ungated kakeratta job.
4. The script calls `list_tasks` before the agent starts. It returns `{"wakeAgent": false}` when the task list is empty, so no LLM call is made. Tasks or pre-check errors return `{"wakeAgent": true}`; Hermes then runs this skill and checks the task state again through MCP. Do not use an output-diff monitor: an unchanged task ID must not suppress the next check.
