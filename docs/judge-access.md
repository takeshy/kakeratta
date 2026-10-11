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

### Submission instructions and final checks

Use the localized, credential-free templates [Japanese](../integrations/alexa/testing-instructions-ja.txt) and [English](../integrations/alexa/testing-instructions-en.txt). Replace the email/password placeholders only in the Console's private Testing Instructions field. Both include help, correction, threaded replies, cancellation before confirmation, stop, and the distinction between reading saved plans and Alexa Reminders. The skill does not use the Alexa Reminders API or automatically announce scheduled plans through Alexa.

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
