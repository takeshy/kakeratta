# Judge access

Kakeratta provides one shared, unguessable registration URL for hackathon judges. Each judge opens that URL and creates a separate Firebase email/password account. The server validates the invitation, adds the `judge: true` custom claim, and refuses password accounts without that claim.

Judge accounts use an operator-owned Nebius Token Factory key stored in Secret Manager and never sent to the browser. Each account may start up to 200 AI actions. The counter is updated atomically in Firestore. Registration and access close at `2026-12-15T20:00:00Z` (`2026-12-16 05:00` in Japan), immediately after the published judging period.

Deploy the infrastructure with `./infra/deploy.sh`, then retrieve the single URL without printing either secret:

```sh
terraform -chdir=infra output -raw judge_url
```

Put this in Devpost testing instructions:

> Open the judge URL, register with any email address and a password of at least six characters, and start using the app immediately. Email verification is not required.

The invitation is intentionally reusable during judging; the URL is the shared gate, while accounts, workspaces, custom claims, and usage counters remain separate. Rotate the Terraform `random_id.judge_invite` resource or remove the judge environment settings to revoke it early.

## English starting workspace

A fresh browser opening the judge invitation defaults to English even if its browser locale is Japanese. The selection is saved before registration and retained after entering the app. Existing display-language choices are preserved; `?lang=en` or `?lang=ja` on the invitation explicitly selects either supported language. Registration and existing-account sign-in share a pending state through workspace and session creation, preventing duplicate submission and keeping progress visible until navigation.

New invitation registrations start with English examples adapted from the owner's October 2026 production timeline. The welcome post identifies them as sample history, not the judge's own activity or fresh AI results. The examples cover an unvisited restaurant in Shizuoka, separate wine preferences for the user and fictional friend Mei, cooking with leftovers and turnips, and gifts for Dad. Original personal names and production entry IDs are not needed in the sample.

Eight English personas are active: **tabe** (dining), **Wine**, **Kitchen**, **Mei**, **Dad**, **sona** (preparation and follow-ups), **support**, and **infographic**. **shiori** (reading) and **Goldfish** are available to activate. Five personas have example memories linked to their source posts. Try “Going to Shizuoka tomorrow,” “What wine would suit Mei?” or “@Kitchen What can I make with turnips and eggs?” To test a direct external request, post “@agent Research three gift ideas for someone who enjoys whisky.”

Only **Wine** initially prefers the external agent when shared Hermes is configured; the other personas use built-in answers. Re-registering preserves per-persona engine choices. The seed includes a selected production wine photo, copied into each judge's own blob storage with a prepared English interpretation. It makes no model calls and copies no production API keys, connections, Push devices, jobs or live schedules. Judges can edit or delete the examples. Existing workspaces with posts or installed personas are left alone; repeat registration does not reset the sample. Interrupted initialization resumes with stable IDs and dates. The [embedded sample](../../internal/server/judge_seed.json) is fixed at build time; registration never reads the owner's production workspace. See [sample personas, posts, memories and suggested checks](judge-sample-data.md) for the complete inventory.

## Alexa certification

The existing judge configuration (invitation, Token Factory key, future expiry and positive usage limit) must remain active throughout certification. Alexa review access also requires the Firestore usage counter. No Google account is needed by the reviewer; provision an email/password account beforehand:

```sh
go run ./cmd/provision-alexa-review \
  -project kakeratta \
  -email YOUR_NEW_REVIEW_EMAIL \
  -credentials-file /tmp/kakeratta-alexa-review-credentials.json \
  -apply
```

Use operator ADC, or pass `-access-token-file` pointing to a private temporary file. Never commit that token or the generated credentials. The command refuses existing accounts/files; if a later step fails, inspect the account and private file before retrying. Sample content is explicitly labelled as an example. The default schedule is 30 days ahead; use `-schedule-at` with a future RFC3339 timestamp if the review window is later. Actual posts still use the normal persona pipeline and operator judge model key.

After deploying the application, first test linking with the supplied review account from the developer's Alexa account. Supply the generated email/password only in the private Testing Instructions field. Suggested instructions:

> Enable Kakeratta Notes and start account linking in the Alexa app. On the Kakeratta login screen, select “Review access / 審査用ログイン”. Enter the test email and password supplied below, then approve account linking. No Google account is required. To inspect the saved notes in a browser, open https://kakeratta.net/?login=1&review=1&lang=en and use the same credentials. Opening this browser URL alone does not link Alexa.
>
> Say “Alexa, open Kakeratta Notes”, then “Read my replies” to hear the example reply, and “Read my schedule” to hear the example plan. Say “Take a note I want to go for a walk tomorrow” to dictate a new note, then say “Yes” after the read-back to save it. Say “No” to dictate it again. After hearing a reply, say “Reply” and dictate a response, then confirm with “Yes”. Replies to new notes are asynchronous; within that session, “Read my replies” refers to the new note. Say “Stop” to exit.

The same account allows one active Alexa connection. A new completed link replaces the previous link. Do not relink it while reviewers are using it. Expiry blocks browser and Alexa access, including token refresh. A quota exhaustion blocks new Alexa posts but permits reading existing replies/plans until expiry; drafts and corrections do not charge quota. Already-saved confirmation retries do not charge again, although concurrent first attempts may consume extra units.

### Submission instructions and final checks

Use the localized, credential-free templates [Japanese](../../integrations/alexa/testing-instructions-ja.txt) and [English](../../integrations/alexa/testing-instructions-en.txt). Replace the email/password placeholders only in the Console's private Testing Instructions field. Both include help, correction, threaded replies, cancellation before confirmation, stop, and the distinction between reading saved plans and Alexa Reminders. The skill does not use the Alexa Reminders API or automatically announce scheduled plans through Alexa.

Before submission, follow the [Amazon submission checklist](https://developer.amazon.com/ja-JP/docs/alexa/custom-skills/certification-requirements-for-custom-skills.html#submission-checklist):

- Deploy the corresponding server/frontend revision and rebuild every submitted locale's interaction model. A successful local test does not validate Amazon's deployed model.
- Check the review account's expiry, remaining action quota and future sample schedule date. Do not submit if the review window would exceed them.
- Verify review login and account linking inside the Alexa app, not only from a standalone web browser.
- Test all three advertised sample phrases, help, draft read-back, rejection/correction, confirmation, latest reply, follow-up reply, stop and cancellation on the submitted locales. Confirm cancelled text is absent from the timeline.
- Verify public legal URLs, localized descriptions and current images. The images describe confirmation before posting and reading the latest single reply.
- Keep unused device address/name/email permissions off. Check Console policy declarations against actual functionality; this checklist does not replace Amazon's policy review.

### Verification on 2026-10-11

Cloud Build `18e7682e-8894-4aa3-92b6-7d7a8f44aea5` deployed revision `8b605c1` successfully. Public Japanese/English privacy and terms URLs returned HTTP 200. The deployed review login module, Firebase password login, browser session, sample reply, OAuth authorization/single-use code exchange/refresh/revocation, and rejection of unsigned Alexa requests were verified against production. The temporary OAuth test connection was revoked afterwards; reviewers must link from Alexa themselves.

The production judge expiry is `2026-12-15T20:00:00Z` and the per-account cap is 200 AI actions. The review account had used zero actions at verification; its example schedule was present at `2026-11-10T22:04:09Z`. The operator reported successful Console model rebuilds and all three unused permissions disabled. Real device speech and end-to-end Alexa account linking remain separate manual checks; HTTP checks do not certify them.

## Shared Hermes for judges

The server's `KAKERATTA_JUDGE_MCP_KEY` is a separate 64-character random secret from the invitation and model API keys. Use the existing Secret Manager reference (`judge_mcp_key_secret_id` / `judge_mcp_key_secret_version`); never put its value in browser configuration, testing instructions, or committed files. New invitation registrations automatically install its hash into the isolated workspace and record the workspace for discovery. The English sample selects only Wine for external answers when the shared connection is configured. Repeating registration repairs the same connection and preserves an existing user-selected connection and per-persona engine choices. Accounts registered before the UID-bearing marker change must register through the invitation again to bind their identity. This preserves their existing workspace data.

On the shared Hermes host, install the current `check_kakeratta_gate.py`, `direct_request_worker.py`, and kakeratta `SKILL.md`. Keep the existing parent model/provider and dedicated `kakeratta_worker` (or `delegation`) settings. Provide these variables to the actual Cron service environment using a private environment file or secret injection:

```text
JUDGE_WORKSPACES_URL=https://kakeratta.net/internal/judge-workspaces
JUDGE_MCP_KEY=<the same secret as KAKERATTA_JUDGE_MCP_KEY>
```

Attach the gate to the existing one-minute Hermes Cron job. Explicit discovery needs no seed judge workspace in `mcp_servers.kakeratta`; it discovers registrations at each tick. The existing alternative, a configured kakeratta MCP URL with the shared key, also discovers judges from that origin automatically. Ordinary per-user keys keep the single-workspace behavior.

Judge requests and persona asks run in detached Hermes processes with separate workspace state, audit logs, sessions and locks under `HERMES_HOME/kakeratta-judge-workspaces/{workspaceId}`. The parent configuration is unchanged. Shared judge runs expose only the selected kakeratta MCP; direct requests additionally have the `web` toolset. They do not inherit the host's local filesystem server, terminal/action toolsets or other MCP connections. The operator's parent `.env` remains available to Hermes for provider authentication. This is application-level separation within one trusted Hermes process environment, not a separate OS sandbox for each judge.

Discovery and shared MCP calls check the current Firebase account, enabled state, judge claim, active connection, operator key and judge period. Delete/disable an account or remove its claim to exclude it. Removing the server key or ending the judge period revokes the shared connection; restart processes after changing environment secrets. Key rotation refuses old credentials immediately after restart; existing connections must be repaired through registration with the new key. Never print the key while checking the API. Keep the shared VM's `/internal/judge-workspaces` proxy route (or the Firebase Hosting rewrite) deployed alongside the server.

Deployment acceptance still requires two new judge accounts and the real Hermes host: confirm next-tick discovery, one persona ask and one `@agent` request per account, separate replies/notes/schedules/Push destinations, and built-in persona fallback with the host stopped. Direct requests retain their existing expiry notice and have no built-in-model fallback. Local fixture tests do not establish those live results.
