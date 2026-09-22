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
