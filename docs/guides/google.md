# Google Bridge: use Google with your own Hermes

[README](../../README.md) · [日本語](google_ja.md)

Google Bridge is a **separate Web app that each user runs on their own device or server**. It provides Google configuration, credential storage and MCP without starting the main kakeratta application. Each instance belongs to one owner.

It supports reads and, on the owner's direct request, sending/replying to email, saving drafts, and creating/updating/deleting Calendar events. Source and tests are in `tools/google-bridge/`. Incremental sync, rule-based importance filtering, Hermes selection and summarization, and candidate delivery to the kakeratta Timeline are also supported. Items enter normal posting, search, persona and memory processing only after the owner chooses **Import** in the Timeline.

## Start Bridge

Use Go 1.26 and this repository. The frontend is embedded in the Go binary, so npm and a separate Web server are unnecessary. The CLI supports Linux/macOS; use WSL on Windows. Run these commands from the repository root, with [mise](https://mise.jdx.dev) and the repository's Go toolchain installed:

```sh
mise exec -- go run ./tools/google-bridge/cmd/google-bridge -init
mise exec -- go run ./tools/google-bridge/cmd/google-bridge
```

The initial `-init` creates `google-bridge/config.json` in the user configuration directory with mode 0600. It prints the file path without printing keys and refuses to overwrite an existing configuration. To use another location, add `-config /absolute/private/path/config.json` to both commands.

1. Open `http://127.0.0.1:8094`.
2. Open the protected configuration file in your editor and enter its `adminToken` in the management login form. Keep this management key out of Hermes and chat.
3. Configure Google and connect Hermes as described below. An MCP key is optional in same-machine local connection mode; issue an individual key for connections from another machine.

Management sessions use an HttpOnly / SameSite=Strict cookie valid for eight hours, with Secure added for HTTPS. A new login invalidates the previous management session. The management key is not stored in browser localStorage or URLs.

For persistent execution and startup after a reboot on Linux, use the provided systemd user service:

```sh
mkdir -p ~/.local/bin ~/.config/systemd/user
mise exec -- go build -o ~/.local/bin/google-bridge ./tools/google-bridge/cmd/google-bridge
cp scripts/nemoclaw/systemd/google-bridge.service ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now google-bridge.service
```

Alternatively, `scripts/nemoclaw/install.sh` installs the service alongside the NemoClaw services and builds Bridge when Go or mise is available. The unit uses `ConditionPathExists=%h/.config/google-bridge/config.json`, so it starts only when the default configuration exists. If you chose a custom configuration path, adjust the unit's condition and command accordingly. To run user services without an active login, enable lingering with `loginctl enable-linger "$USER"`. WSL also requires Docker Desktop and the WSL distribution to start; see the [NemoClaw operations guide](../../scripts/nemoclaw/README.md).

## Initial configuration file

| Field | Meaning |
| --- | --- |
| `listen` | Listening address; defaults to `127.0.0.1:8094` |
| `publicUrl` | Canonical origin used by the browser and Hermes; defaults to `http://127.0.0.1:8094`. No path, query or fragment |
| `dataDir` | Absolute path dedicated to encrypted credentials and authentication hashes. Do not reuse kakeratta's data directory |
| `adminToken` | Random 64-character hexadecimal management key generated during initialization |
| `keyVersion` | Current encryption-key name; initially `v1` |
| `keys` | Map from key names to 32-byte keys represented as 64 hexadecimal characters, randomly generated during initialization |

Do not put Google client secrets or access/refresh tokens in this file. Register them through the management form; Bridge encrypts them with AES-256-GCM inside `dataDir`. The configuration contains encryption keys, so keep it owner-only and outside the database directory. Startup validates permissions and placement. A file lock prevents multiple processes from using the same data directory.

For remote access, set `publicUrl` to your HTTPS reverse proxy's origin and preserve that Host when forwarding. A non-loopback listener requires a public HTTPS origin. Bridge does not terminate TLS; protect its internal HTTP port with a firewall or equivalent. Exclude the query string of the OAuth callback `/api/google/callback` from proxy access logs. Bridge itself does not generate access logs.

## Set up your Google project and OAuth

The Web UI follows the browser language, Japanese or English; add `?lang=en` or `?lang=ja` to the URL to choose one. Under **Guide me through Google Cloud setup**, specify a project ID and the services to use. Choosing a new project displays steps including `gcloud projects create`. API enablement and verification commands explicitly target that project. Bridge displays commands for you to run on your own machine; it does not execute them or store Google Cloud CLI login credentials. Check the displayed account and project before running them.

If the CLI is unavailable or permissions or organization restrictions block it, use the Console links in the same panel. Compare the displayed API names with `gcloud services list --enabled`. Displaying instructions does not mean those steps have run. Consent-screen, test-user and OAuth-client creation are covered by manual instructions. See Google's references for [creating projects](https://docs.cloud.google.com/sdk/gcloud/reference/projects/create), [enabling APIs](https://docs.cloud.google.com/sdk/gcloud/reference/services/enable), and [listing enabled APIs](https://docs.cloud.google.com/sdk/gcloud/reference/services/list).

1. In Bridge, find **Redirect URI to register**. The default is `http://127.0.0.1:8094/api/google/callback`.
2. Create or select your Google Cloud project. Enable **Gmail API** for mail and **Google Calendar API** for events.
3. In **Google Auth Platform**, configure **Branding**, **Audience**, and **Data Access**. For an **External / Testing** app, add yourself as a test user.
4. Under **Clients**, create an OAuth client of type **Web application**. Add Bridge's exact redirect URI to **Authorized redirect URIs**. Use Web application even when running the Go server and browser locally.
5. Enter the project ID, client ID and client secret in Bridge's shared connection form, or select the Web OAuth configuration JSON downloaded from Google. Files are limited to 64 KiB. The browser checks the client type and exact redirect URI before filling the fields. It does not adopt authentication URLs from the JSON or persist the file or secrets in logs or browser storage. Only the required settings are sent to the authenticated API when connecting, then encrypted. Keep Google settings out of chat.
6. If needed, select **Calendar: also allow reading the calendar list** or the extra permissions for sending mail and changing events. Basic read access needs no extra selection.
7. Read the displayed data flow, consent to sharing Google data with Hermes and its configured model, then press **Connect to Google**. One Google login and consent flow authorizes both Gmail and Calendar.
8. When you return to the original tab, Bridge completes authorization and tests both APIs with small read requests. Check each service's status, Google account, scopes and last verification time in the UI.

Gmail requests `gmail.readonly`; Calendar requests `calendar.events.readonly`, with `calendar.calendarlist.readonly` only when calendar-list access is selected. `openid` and `email` support account matching and display; Bridge compares the subject returned by Google's fixed userinfo endpoint. Use the same Google account for both services.

In Testing mode, Gmail/Calendar refresh tokens generally expire after seven days. Personal-use verification exceptions exist, but owning the project alone does not guarantee an exemption. See Google's [verification guidance](https://developers.google.com/identity/protocols/oauth2/production-readiness/restricted-scope-verification) and [token-expiration rules](https://developers.google.com/identity/protocols/oauth2#expiration).

The UI provides recovery guidance when connection, reauthorization or testing fails. For `redirect_uri_mismatch`, check the exact URI in Clients. For `access_denied`, check whether consent was cancelled, the account is a test user, or organization policy blocks access. Reconnect in the same tab for expired authorization; add permissions for missing scopes. If the OAuth JSON does not match, correct Google's settings and download it again. Manual entry and reauthorization with the saved client remain available.

## Add Bridge to Hermes

Save the example under **Connect to Hermes** in your Hermes configuration. Same-machine local mode can connect without Authorization. If you issue an individual key, use the value shown once in the UI. This keyed example contains no Google credentials. The Bridge MCP key, management key and kakeratta MCP key are separate; MCP keys cannot change Web settings.

```yaml
mcp_servers:
  google_bridge:
    url: http://127.0.0.1:8094/mcp
    headers:
      Authorization: Bearer YOUR_PERSONAL_MCP_KEY
    tools:
      prompts: false
      resources: false
```

Replace the placeholder with the issued key, then restart Hermes. Keep it out of chat and public repositories. Rotating or revoking a key rejects subsequent requests using the old key; a short authenticated read already in progress may still finish.

If Hermes runs on another machine or inside a container, `127.0.0.1` refers to Hermes itself. Provide SSH port forwarding that preserves the configured origin, or run Bridge behind your own HTTPS server. Hosts that differ from `publicUrl` are rejected to prevent DNS rebinding. For NemoClaw, configure sandbox connectivity and its network policy as well; the kakeratta setup script does not configure the separate Bridge connection for you.

To use Bridge from kakeratta's direct-request worker, add `google_bridge` to Hermes's existing `kakeratta_worker.mcp_servers` extension setting. Workers using extra MCP servers do not automatically retry failed inference under the existing rules. Extra MCP servers remain excluded from shared judge workers and persona-ask homes. No changes to the distributed worker or kakeratta implementation are required.

| Tool | Example input |
| --- | --- |
| `google_connection_status` | `{}` |
| `gmail_search` | `{"query":"is:unread newer_than:7d","maxResults":10}` |
| `gmail_get` | `{"messageId":"id-from-search"}` |
| `gmail_thread` | `{"threadId":"threadId-from-search"}` |
| `calendar_events` | `{"calendarId":"primary","timeMin":"2026-10-19T00:00:00+09:00","timeMax":"2026-10-26T00:00:00+09:00"}` |
| `calendar_get` | `{"calendarId":"primary","eventId":"event-id"}` |
| `calendar_list` | `{}` (requires calendar-list permission) |

For example, ask Hermes to find reservation-related messages among your unread Gmail. Tool results reach Hermes and its configured model and follow their conversation-history and provider-retention policies. Bridge does not persist message bodies from interactive reads or automatically post those reads to kakeratta. Only enabled intake with Hermes-relayed candidates creates pending Timeline cards. Content Hermes quotes, stores or posts follows Hermes's and the destination's retention policies.

Gmail returns at most 50 messages per search, 20 messages per thread and 16,000 plain-text characters per message. Attachments are not fetched; HTML-only messages return a snippet. Read/unread state is only inspected. Calendar queries return at most 50 events over a maximum 366-day range. Continue with the same filters and returned `nextPageToken`. Search/event-list responses are under `data`; message-detail/thread responses return their information directly.

Mail, subjects, senders and event descriptions are untrusted external data. Do not treat them as owner instructions or allow their contents alone to trigger URL access, forwarding or commands. Tool responses include provenance and handling guidance. Writes are reserved for direct owner requests to Hermes. Configure Hermes to keep external content separate from instructions.

## Connection flow: Hermes uses both MCP servers

```mermaid
sequenceDiagram
  participant C as Hermes cron (no-agent)
  participant W as Hermes Python worker
  participant M as Hermes selection model
  participant B as Google Bridge
  participant K as kakeratta MCP
  participant U as Owner's Timeline
  C->>W: Gate starts worker only when work exists
  W->>B: gmail_sync / calendar_sync
  W->>B: triage_context (when selection is pending)
  W->>M: Candidates and owner's selection instructions
  M-->>W: Selection and summary JSON only
  W->>B: triage_submit (submitted by code)
  W->>B: google_review_context
  B-->>W: Candidate tool, arguments and token
  W->>K: propose_gmail_import / propose_calendar_import
  K-->>W: Actual receipt
  W->>B: google_review_receipt
  K-->>U: Pending import card
  U->>K: Import / Discard
  W->>K: Poll the same candidate on a later run
  K-->>W: Owner's decision and exclusion rules
  W->>B: Return decision through google_review_receipt
```

**Bridge never contacts kakeratta directly or stores its URL or connection key.** Delivery belongs to the Hermes-side `google_bridge_worker.py`. It reads Bridge settings from private `google_bridge.json` and kakeratta settings from `mcp_servers.kakeratta` in the parent Hermes `config.yaml`, supporting `${VAR}` and `env:VAR` references. Connection keys never enter model sessions. The `mcp_servers.google_bridge` entry supports interactive reads and direct requests to Hermes. Register the separate kakeratta connection using the [Hermes guide](hermes.md); do not share keys between Bridge and kakeratta.

At startup, updated Bridge versions remove destination credentials saved by the previous direct-delivery implementation. Cards already delivered to kakeratta remain available. Undelivered candidates remain in Bridge's existing cache for Hermes to fetch. Downstream owner review and deduplication remain unchanged.

## Review and import important mail

1. Under **03 / Important mail**, check whether intake is enabled. If stopped, press **Turn on with recommended settings**. A fresh local setup enables intake and model selection; stop them if unwanted. Filters control target labels, priority labels/senders, exclusions, stored body length and retention. Defaults are INBOX, IMPORTANT/STARRED priority, threshold 50, 2,000 body characters, seven-day retention and a one-day initial lookback.
2. Press **Fetch new mail now** to inspect up to 20 messages per batch. Spam and trash are excluded; From exclusions apply before model access. This action alone does not deliver anything to kakeratta.
3. With model selection enabled, the worker fetches up to ten candidates and the owner's instructions through `gmail_triage_context`. An isolated Hermes model session only selects and summarizes. It returns indexed JSON; code binds the results to original IDs/tokens and submits `gmail_triage_submit`. Kept summaries omit signatures, boilerplate, disclaimers, unsubscribe notices, advertising and quoted history. Settings changes invalidate stale decisions. Selection alone neither imports nor delivers a message.
4. Worker code calls `google_review_context` with `{"service":"gmail"}`, receiving up to five tool/arguments/token payloads. It passes each candidate's **unchanged arguments** to `propose_gmail_import` through its own kakeratta MCP connection. This creates only a pending owner-review card.
5. The worker returns kakeratta's actual `structuredContent` to Bridge's `google_review_receipt` as service/token/receipt. A missing success response leaves the candidate retryable. Connection ID plus message ID prevents duplicate cards after lost responses.
6. In the Timeline's pending Gmail card, choose **Import** or **Discard**. Import starts normal posting, routing, persona and memory processing. Pending cards stay out of search and persona processing. Instructions or `@agent` text inside mail do not become direct owner requests.
7. Discard deletes the pending body. The separate **Exclude this type** and **Exclude this From** checkboxes apply only when discarding. Worker code polls the same candidate at kakeratta and returns the actual receipt before Bridge applies exclusions. Remove a row and save in Bridge to cancel an exclusion; replayed old receipts do not restore cancelled rules.

`google_review_context` prioritizes new candidates and rotates pending-card polls through remaining slots. Stopping intake, disconnecting, deleting stored data or expiry removes stale candidates from delivery. Fetch fresh context after changing body-storage limits. The receiver preserves an existing card and returns the owner's decision rather than silently rewriting it.

Bridge retains at most 200 selected bodies for the configured number of days, pruning on later reads/syncs. Pending bodies delivered to kakeratta remain there until the owner decides; deleting Bridge data does not recall them. Initial lookback advances to Gmail history-based incremental sync; expired history falls back to the configured lookback. Processed IDs and receiver receipts prevent duplicates. Complete recovery of messages older than that range is not guaranteed.

### Schedule mail intake with Hermes cron

Fetching, model-result validation/submission, delivery and receipt forwarding run in **Hermes-side Python code**. The model only selects and summarizes and has no MCP tools. Use `--no-agent` so the cron parent model does not start either.

1. Copy [check_google_bridge.py](../../tools/google-bridge/hermes/check_google_bridge.py), [check_calendar_bridge.py](../../tools/google-bridge/hermes/check_calendar_bridge.py), and [google_bridge_worker.py](../../tools/google-bridge/hermes/google_bridge_worker.py) into your `HERMES_HOME/scripts/` (normally `~/.hermes/scripts/`; `/sandbox/.hermes/scripts/` in the NemoClaw sandbox).
2. Install the [Hermes integration](hermes.md) first, including `direct_request_worker.py` in the same scripts directory. The Google worker reuses its model settings, Hermes CLI runner and usage/deadline monitoring. It selects the parent Hermes model and allows one turn per selection session, at most five model batches/API calls, 250,000 tokens and a 300-second model deadline per invocation. Each batch has up to ten candidates. An in-flight call can exceed the observed token budget.
3. Save the JSON shown when issuing a Bridge MCP key as sibling `google_bridge.json` with mode 0600. Include **only the Bridge URL and key**. Same-machine keyless mode can use `{"url":"http://127.0.0.1:8094/mcp","key":""}`; this loopback example requires forwarding if Hermes is sandboxed.
4. Configure your own `mcp_servers.kakeratta` in the parent Hermes `config.yaml`. Worker code reads that URL and headers. The selection model's isolated home has no MCP servers, skills or external tools.
5. Register the following in your Hermes environment. Replace old agent-based delivery jobs to avoid duplicate schedules.

```sh
hermes cron create "every 30m" --no-agent --script check_google_bridge.py --deliver local --name google-bridge-mail
```

The gate examines sync counts and starts the code worker when selection, delivery or receipt polling is pending. It always returns `wakeAgent:false`; idle and delivery-only runs use no model calls. Per-service locks prevent concurrent workers. Each invocation drains up to ten sync batches without repeatedly polling the same pending card.

After validating model results, the worker saves them to mode-0600 `HERMES_HOME/google-bridge/<service>-decisions.json` before submission. This journal stores generated summaries, classifications, IDs and tokens, without original bodies. Transport failures retain it for submission without another model call. Completion or a known context/consent invalidation deletes it; temporary storage failures retain it for retry. Model session history may retain candidate text under Hermes's retention policy. Lost delivery responses are retried using the same Bridge candidate and stable identities.

`<service>-status.json` stores successful counts only; `<service>-worker.log` contains fixed failure guidance. Original bodies, summaries and keys stay out of cron output and notifications. Destinations, tool names and receipts come from configuration, Bridge candidates and actual kakeratta responses, never model output. Bridge trusts its authenticated worker to return faithful receipts and holds no kakeratta connection itself.

## Select and import today's and tomorrow's Calendar events

1. Connect Calendar with read permissions. The `primary` calendar does not require calendar-list permission.
2. Under **04 / Bring in today's and tomorrow's events**, check intake status and press **Turn on with recommended settings** if needed. Advanced settings choose calendar ID, timezone (default `Asia/Tokyo`) and description length (default 2,000 characters). Event fetching needs neither Gmail intake nor a destination configured in Bridge.
3. Press **Check today's and tomorrow's events now**. Bridge fetches events overlapping midnight today through midnight the day after tomorrow in the configured timezone. Recurring events expand into instances; all-day end dates are exclusive.
4. The worker fetches `calendar_triage_context`, validates model selection/summaries and submits `calendar_triage_submit` in code. It then requests `google_review_context` with `{"service":"calendar"}` and forwards the returned arguments to its kakeratta MCP `propose_calendar_import`. Actual receipts return through `google_review_receipt`.
5. Choose Import or Discard in the Timeline. Discard with an exclusion checkbox matches calendar ID plus event ID for one occurrence, or calendar ID plus master ID for a recurring series. Exclusions apply in Bridge before model access once the worker returns the actual receipt. Remove an exclusion in Bridge and save to allow future new occurrences; processed occurrences are not presented again.

Normal Calendar sync does not refetch Google after a completed sync on the same local calendar day; the Web button can refresh that day. Later Google edits do not update existing cards or posts. Fetching, selection and delivery never write to Google Calendar. Cached events are removed on later reads/syncs once 24 hours past their end or seven days past acquisition. Google events and cards/posts already delivered to kakeratta remain untouched.

### Schedule the daily Calendar job

Use the same private JSON, worker, existing persona-worker runtime and personal kakeratta connection as mail. This example runs at 06:00 in the Hermes cron timezone. Bridge's configured IANA timezone defines the dates to fetch.

```sh
hermes cron create "0 6 * * *" --no-agent --script check_calendar_bridge.py --deliver local --name google-bridge-calendar
```

This worker never calls Google write tools or the owner-review decision API. Calendar delivery and receipt-only runs use no model tokens; enabled selection uses an isolated session. Live Gmail and Calendar integration through Hermes has been verified by the project owner.

## Send mail and change events on direct request

Under **Allowed actions**, choose the permissions to grant when connecting Google. All options are initially selected. Sending uses `gmail.send` (Sensitive); drafts use `gmail.compose` (Restricted); Calendar changes use `calendar.events` and `calendar.calendarlist.readonly` for ownership checks. Google's compose scope also permits sending. See the [Gmail scope reference](https://developers.google.com/workspace/gmail/api/auth/scopes).

1. Ask your own Hermes directly, for example to send someone an email or add an event tomorrow at 10:00.
2. Hermes calls `google_action_run`, which executes without another Web confirmation. `userRequest` contains your verbatim request and is retained in the audit log. Use a unique 16–128-character `idempotencyKey` for each operation, reusing it only for retries of that same operation. Retries return the saved result rather than sending twice.
3. Review the operation and request under the execution history.

Tool descriptions and MCP instructions reserve this tool for direct owner requests. Mail, event descriptions, other people's Timeline posts, cron/selection jobs and agent initiative must not trigger writes. This relies on Hermes and its model following instructions; the server cannot verify the request's origin. Exclude `google_action_run` from selection cron jobs using Hermes settings such as `tools.exclude`.

Email example:

```json
{"type":"gmail_send","idempotencyKey":"mail-operation-example-001","mail":{"to":["recipient@example.com"],"cc":[],"subject":"Confirmation","body":"Please review these details."}}
```

For a reply, set `mail.replyToMessageId` to the original message ID and use the original subject, optionally prefixed with `Re: `. Bridge retrieves and fixes the original Message-ID and thread. Always specify recipients explicitly. `gmail_draft` separately saves a draft at Google. Sending by saved draft ID is not provided; sending is a new operation with a fixed body. The sender is the connected account only. To+CC is limited to 20 recipients and the UTF-8 body to 16,000 bytes. Attachments, BCC and HTML sending are unsupported.

Event creation example:

```json
{"type":"calendar_create","idempotencyKey":"calendar-operation-example-001","calendarId":"primary","sendUpdates":"all","createMeet":false,"event":{"summary":"Discussion","description":"Review the details","location":"Online","start":{"dateTime":"2026-10-20T10:00:00+09:00","timeZone":"Asia/Tokyo"},"end":{"dateTime":"2026-10-20T11:00:00+09:00","timeZone":"Asia/Tokyo"},"attendees":[{"email":"guest@example.com"}]}}
```

`calendar_update` also requires `eventId` and replaces all supported fields: summary, description, location, start, end and attendees. Omitted values become empty, so include values you want to retain. Other fields and existing meeting links are preserved. `calendar_delete` takes the target ID without an event object. Changes are limited to calendars where the connected user is the owner. Whole-series changes, special event types and delegated administration are rejected. Individual recurring instances can be changed or deleted; creating new recurring events is unsupported.

Timed events require RFC3339 offsets and matching IANA timezones at both ends. All-day dates use values such as `{"date":"2026-10-20"}` with an exclusive end date. At most 20 attendees are supported. Explicitly choose `sendUpdates` from `none`, `all` or `externalOnly`; Google may still send some notifications with `none`. `createMeet:true` applies only to new events on calendars where Google permits Meet creation. See Google's [event insertion reference](https://developers.google.com/workspace/calendar/api/v3/reference/events/insert).

Updates/deletes recheck the ETag captured when the operation was prepared and send If-Match with the write. If the event changes, review it and make a fresh request. See Google's [conditional modification guidance](https://developers.google.com/workspace/calendar/api/guides/version-resources). Reconnection and disconnect invalidate unexecuted operations.

If a network failure or timeout leaves the outcome unclear, the operation becomes `unknown` and is never automatically resent. **Check sent mail, drafts or events at Google yourself** before requesting a new operation if needed. Exactly-once delivery is not guaranteed. Saving the consumed decision before execution prevents duplicate sends through app-side retries.

Each service holds at most eight unfinished operations. Proposals are encrypted; body/event payloads are erased on completion, rejection, disconnect, or the next journal access after expiry. No custom background loop wakes solely to expire them. Audit metadata (operator, connection ID, operation type, hashes, result, decision/execution timestamps) and idempotency-key hashes remain to prevent replay. Disconnect a service to delete its stored Google data. To remove all app data including audit records, stop Bridge and remove its dedicated `dataDir`; doing so also removes idempotency history, so do not reuse old operation keys. Backups and Google-side mail/events are managed separately by the owner.

## Reauthorize, disconnect and recover

- To reuse a saved OAuth client during reauthorization or connection of the second service, leave all three client fields empty. Model-sharing consent is requested again.
- **Test connection** searches at most one Gmail message and one Calendar event in the next 24 hours. It does not return bodies to the UI.
- A normal disconnect removes the selected service's credentials and pending OAuth, erases unfinished operation payloads and invalidates those operations. The other service remains connected.
- Selecting **Also revoke access on Google** attempts revocation. This affects **all clients in the same project**, so the other service or other tools may need reauthorization. Local credentials are removed even if revocation fails. Use separate projects if independent revocation is essential.
- Resolve `redirect_uri_mismatch` by matching the registered URI exactly to Bridge's displayed value.
- When reauthorization is requested, check Testing expiry, user revocation and Gmail password changes, then reconnect.
- Add calendar-list permission if only that scope is missing. Disabled APIs and organization policies can also block access.
- Finish OAuth in the same tab within ten minutes. Codes are single-use; restart the connection flow after failure.
- If you lose the management key, stop Bridge and replace `adminToken` in its protected configuration with a new random value. Sessions bind to the management-key fingerprint, so restarting after a change invalidates existing browser sessions. MCP keys are independent; revoke them separately in the UI if needed.

Credentials, refresh, disconnect, sync and transfer operations serialize through the same workspace lock. Disconnect does not recall data already passed to Hermes or delete the Google Cloud project.

For key rotation, retain old keys in `keys`, switch `keyVersion`, then restart. Run connection tests and refresh operation proposals to rewrite connection/action journals, then refresh stored mail/event lists before retiring the old key. Keep the configuration file out of data backups. Retention/deletion of old backup ciphertext follows your backup policy.

## Verification scope

The project owner has verified the complete live Gmail and Calendar integration, including Google OAuth and Hermes connectivity. The hackathon gallery has been updated with captures from that verification. This confirmation covers Gmail and Calendar intake; it does not establish live verification of every send, draft or event-modification operation.

Automated tests use fake Google HTTP to cover OAuth, scopes, instance isolation, encryption, refresh races, revocation, disconnect, MCP, owner-review boundaries, tampering/expiry rejection, concurrent execution, unknown-outcome retry prevention, event ETags, reply MIME, Calendar creation/update/deletion, incremental sync, expired history, resume, retention, Hermes-relayed Timeline review/Discard/deduplication/search isolation, absence of direct Bridge delivery, and removal of legacy destination credentials at startup. Browser checks cover login, key issuance, MCP initialization, revocation, required consent, mobile layout and logout. Automated tests do not require or modify a real Google account.

After configuring your own instance, check Gmail search/details and a Calendar date-range query once. Sending mail and modifying events are separate operations; request them explicitly only when needed.
