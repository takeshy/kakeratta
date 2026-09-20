# Using Hermes Agent as a persona

[README](../README.md) · [日本語](hermes_ja.md)

Kakeratta stores requests as an MCP server; Hermes connects to it, fetches the requests, runs them, and reports back. Hermes's models, tools, skills, and long-term memory are managed on the Hermes side. Installing and configuring Hermes itself is a separate step.

## Connecting

1. In Kakeratta, open **Settings → Advanced options → Integrations** and select **Create connection key** under **Hermes integration**.
2. Add the configuration shown there to your Hermes `~/.hermes/config.yaml`. If a `mcp_servers` section already exists, add `kakeratta` under it. The configuration already contains the connection URL and the connection key.

   ```yaml
   mcp_servers:
     kakeratta:
       url: "https://kakeratta.net/api/mcp/users/xxx"
       headers:
         Authorization: "Bearer THE_ISSUED_KEY"
   ```

3. The key is shown only on the screen right after it is issued. Because the configuration contains the key, do not share it with anyone. If you reissue the key, replace the entry in `config.yaml` with the new configuration.

4. Restart Hermes and enable Hermes under **Personas** in Kakeratta.
5. Post a request from **Ask Hermes**. Posting `@Hermes tidy up the README structure` directly also invokes it. Ordinary posts are routed by the Router's persona conditions, and a reply to a Hermes response also becomes a request.
6. Tell Hermes to "check the Kakeratta requests". To have it respond automatically, set up a scheduled run on the Hermes side that checks for requests.

The URL is **the Kakeratta URL reachable from Hermes**. You do not need to expose Hermes's API server, and Kakeratta never starts the Hermes process.

For the configuration format, see the [official Hermes MCP guide](https://hermes-agent.nousresearch.com/docs/user-guide/features/mcp) and the [MCP configuration reference](https://hermes-agent.nousresearch.com/docs/reference/mcp-config-reference).

## The key and its scope

The key is shown only on the screen right after it is issued; the server stores a hash. Reissuing invalidates the previous key, and **Disconnect** revokes it. Browser login cookies cannot be used for MCP authentication.

The key can read the issuing user's timeline, persona list, and each persona's notes. It can also save Hermes's notes, fetch, cancel, and complete requests, and post reports under Hermes's name. It cannot access model settings or other users' data, and it cannot rewrite other personas' notes.

MCP tools can read and write only while the Hermes persona is ON. When it is OFF, access to every MCP tool is refused, including the timeline, persona list, notes, and requests. The key is kept, so turning the persona back ON makes it usable again. While OFF, connection initialization and listing tool definitions still work, but no user data is returned.

**Disconnect** erases the stored key hash and invalidates authentication for the connection itself. Reconnecting requires reissuing the key and updating `config.yaml` on the Hermes side. Turning the persona OFF or revoking the key does not erase data Hermes has already fetched, nor does it stop external operations already in progress.

## Processing requests and retrying

Hermes processes requests with the following MCP tools.

1. `list_tasks` lists pending and claimed requests.
2. `claim_task` takes a `taskId` and a `claimId` that is unique per execution. If the response is lost, retry with the same `claimId`. The response includes the target thread and the persona's instructions.
3. Do the work with Hermes's own tools. Do not rerun a claimed request just because Hermes reconnected. Claims never expire on their own.
4. `complete_task` takes the `taskId`, the `claimId`, and the result `text`. An empty body completes the request without posting. The body is limited to 32 KB.

Results are stored before they are delivered to the timeline. If delivery fails, do not redo the work; retry the same completion call, or use `list_tasks` to redeliver undelivered results. The first accepted result cannot be changed later. On transient read failures, requests and undelivered results are kept; a request is invalidated only once its deletion or edit has been confirmed.

### If Hermes crashes after claiming

`list_tasks` and `claim_task` include the following recovery information with each request.

```json
{
  "id": "task_...",
  "status": "claimed",
  "claimId": "execution-...",
  "claimedAt": "2026-09-20T03:00:00Z"
}
```

`claimedAt` is the time (UTC) of the first claim and does not change on retries with the same `claimId`. Requests claimed by an older version have an unknown time and return `null`. `state` and `claim` are also returned for compatibility with existing clients.

In a sequence such as `claim_task → GitHub operation → crash → complete_task never called`, Kakeratta cannot determine whether the external operation completed. Exactly-once execution of external operations is not guaranteed. Claims have no expiry, and reclaiming with a different `claimId` is refused. Being able to fetch the state again with the same `claimId` does not mean the external operation may be rerun.

When the outcome is unknown, stop automatic reruns and have the user check the external state, such as GitHub. If a rerun is needed, call `cancel_task` on the user's instruction, and then have the user **post the request again as a new post**. Even with the same text, a new post is a separate request. A cancelled request cannot be reclaimed.

`cancel_task` cancels unneeded pending or claimed requests, but it cannot stop work already running on the Hermes side. The request box holds up to 128 pending requests, and the most recent 8 delivery records of completed requests are kept.

Hermes's work does not go through Kakeratta's usual workflow review. Configure the approval policy for external operations on the Hermes side. Retrying a reply from the timeline does not rerun the request.

## Daily check-ins and unprompted reports

When you set a preferred check-in time on the persona screen, the Kakeratta server stores a check-in request. The actual check happens when Hermes fetches that request. Automatic handling requires Kakeratta to be running and a scheduled run on the Hermes side. If there is nothing to report, complete with an empty `complete_task` body.

Reports from Hermes's own cron or skills use the MCP tool `post_report`. The following is an example of the tool call arguments.

```json
{"id":"release-check-2026-09-19","text":"There was a release related to yesterday's issue."}
```

Make `id` unique per report and reuse the same ID when resending the same result. The same ID never posts twice, and it does not revive a deleted post. Setting `replyTo` to the ID of an existing post makes the report a reply. The body is limited to 32 KB. This is not a dedicated REST posting API; it is a tool called over the authenticated MCP connection.

## Conversation, memory, and verifying the setup

`get_timeline` and `get_thread` read posts, and `list_notes` and `read_note` read each persona's shared notes. `write_note` saves only into Hermes's own note area. Kakeratta does not manage Hermes's conversation session IDs. Hermes's history and long-term memory are independent, so deleting a post in Kakeratta does not delete information Hermes has already fetched.

After connecting, first confirm that Hermes can read the timeline and claim and complete a small request. In a Firebase-authenticated environment, the server returns 503 right after startup while workspace restoration is still in progress, so retry later.

The regression tests verify that transient read failures do not cancel pending or claimed requests, that stored results are not marked delivered, that retries work after recovery, and that confirmed edits and deletions invalidate requests.
