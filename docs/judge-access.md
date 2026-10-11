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
