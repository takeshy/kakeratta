# Publish a conversation to Bluesky

[README](../README.md) · [日本語](social-sharing_ja.md)

Each user connects their own Bluesky account. Open **Bluesky connection** in Settings or **Share to Bluesky** on a timeline post, enter your handle, and authorize on Bluesky. Kakeratta does not ask for your Bluesky password or an app password. See the [illustrated manual](https://kakeratta.net/manual/?lang=en#en-bluesky).

Select the persona replies and your follow-ups to publish. Review the text, images, reply parents, and account in the preview, then press the publish button. Connecting or reconnecting alone never publishes anything.

- Only the original post starts selected. Persona replies, including replies arriving later, are never automatically selected.
- Persona messages receive an attribution such as `🍚 tabeからの返信:` (reply from tabe).
- Select the parent of each selected reply too. Excluded parents are not automatically published, and replies are not reparented.
- Independent persona replies remain separate replies to the original post.
- Publication includes the selected messages' text, links, source URLs, and attached images. It does not add location metadata or image analysis to the text, or read persona memories.
- Select up to 100 messages. Long messages split automatically at sentence endings or line breaks into posts of up to 300 graphemes and 3,000 UTF-8 bytes, including attribution. Later parts start with `つづき:` (continued) and repeat persona attribution. An oversized sentence splits at commas or spaces, falling back to grapheme boundaries only when necessary.
- URLs and graphemes such as emoji stay intact; an individually oversized URL raises an error. Images appear only on the first part. Images over 2 MB are resized and compressed as JPEG for sharing; saved originals remain unchanged. Transparency becomes white and GIFs become still images. Replies attach to the final part of their parent message. Review the resulting posts and count in the preview.
- Complete HTML persona answers (including a single HTML code fence) are converted to images before preview. Review every image page; long documents become a chain of image posts, and replies attach to the last page. The saved HTML stays unchanged. Only selected answers are converted. Conversion failure or an excessively large document stops publishing; HTML source is never sent as the persona post text. Images are generated in your browser using local fonts and inline/data assets, so external images and fonts remain blocked. Up to ten image pages per answer are supported.
- Changes to the account or content after preview require another preview.
- Disconnect in Settings. Disconnecting or deleting a message in kakeratta does not delete posts on Bluesky.

## Operator setup

Users do not need developer registration or API keys. The app serves OAuth client metadata from its own public URL and uses the official Indigo Go SDK, following the [Bluesky OAuth guide](https://bsky.network/docs/oauth-client/).

Set these server environment variables:

| Variable | Value |
| --- | --- |
| `KAKERATTA_PUBLIC_URL` | The app's public HTTPS origin, such as `https://kakeratta.net`, without a path, trailing slash, or query |
| `KAKERATTA_BLUESKY_KEY` | A random 32-byte secret encoded as 64 hexadecimal characters; share the same value across instances and keep it in Secret Manager or equivalent storage |

Generate a key with `openssl rand -hex 32`. It signs OAuth client requests; a separately derived key encrypts tokens with AES-GCM. Preserve the existing key: losing or replacing it makes stored connections unreadable.

The repository's Terraform generates the key, stores it in Secret Manager, and configures Cloud Run. The public URL defaults to `https://<domain>` and can be overridden with `bluesky_public_url`. Code changes alone do not update production. Update the server and frontend using the existing deployment procedure.

`/api/social/bluesky/client-metadata.json` on the public origin must be reachable without login. It exposes the public key, requested permissions, and callback URL, never the secret key or user tokens. The existing Firebase Hosting `/api/**` rewrite forwards it to the API.

The authorization callback is `/api/social/bluesky/callback`. Firebase Hosting forwards only the `__session` cookie, so a temporary secret in browser sessionStorage binds authorization completion to the initiating tab. Completion by another user or tab, and callback reuse, are rejected. Complete authorization on the same public origin and in the same tab.

For local development, set `KAKERATTA_PUBLIC_URL=http://127.0.0.1:<port>` to use the SDK's loopback OAuth flow. Open the browser at that same origin and configure a proxy to the API. The secret key is still required to encrypt local connection data.

## Authorization and storage

The requested scopes are `atproto`, `repo:app.bsky.feed.post?action=create`, and `blob:image/*`. Indigo handles PKCE, DPoP, PAR, token refresh, and handle/DID/PDS resolution. The connection status API returns the account DID and handle, never tokens.

Connections are encrypted and stored per user workspace: locally in `social-state/`, or in Firestore at `users/{userKey}/socialState/`. Pending authorization expires after ten minutes. Expiring workspace locks serialize OAuth and publication operations to prevent refresh-token races between Cloud Run instances. Run only one server process per workspace with the local store.

Disconnecting invalidates stored connections, sessions, and pending authorization, and attempts to revoke tokens on Bluesky. If remote revocation fails, the UI asks the user to revoke access from Bluesky settings.

Images upload directly to the authorized Bluesky API. No unauthenticated endpoint is added for kakeratta images.

## Publication history and partial failure

The app stores a URI and CID for each destination, Bluesky DID, entry ID, and part number. The first part retains the original history key; subsequent parts use suffixes such as `:part:2`. Reusing a conversation skips already published messages and uses them as parents for new replies. Histories are isolated between users and accounts.

- Local: `publications/*.json`
- Firestore: `users/{userKey}/publications/{keyHash}`
- Explicit rejection or an image-upload failure can be retried, continuing after the successful messages.
- A lost connection or server error while submitting a post, or failure to save history after success, leaves the outcome uncertain. The record remains `pending` and automatic resubmission stops. Check Bluesky and have an administrator repair the record if needed. Change it to `retryable` only after confirming that no post was published; otherwise save its URI/CID and mark it `posted`. There is no UI for repairing history yet.

## Validation

```sh
go test ./...
node --test tests/*.test.js
npm --prefix frontend run build
```

Tests simulate the Bluesky API and cover authorization, connection, publication, disconnection, user isolation, encrypted storage, branching replies, excluded messages, preview changes, and recovery after partial failure. They do not publish to a real account. Real-account authorization and publication remain unverified.
