# Connecting the Alexa skill

[Japanese](./alexa_ja.md)

The Japanese and English custom skill supports voice posts, reading persona replies and reading schedules. Deploying the code alone does not make it available in Alexa: create the skill in Amazon's console and configure it as follows.

## Supported languages and regions

One skill and HTTPS endpoint support six locales. Add each language in the console, import its JSON interaction model and build it.

| Language / region | Interaction model | Invocation name |
| --- | --- | --- |
| Japanese / Japan | [ja-JP.json](../../integrations/alexa/ja-JP.json) | 欠片ノート (spoken: かけらノート) |
| English / US | [en-US.json](../../integrations/alexa/en-US.json) | memory timeline |
| English / UK | [en-GB.json](../../integrations/alexa/en-GB.json) | memory timeline |
| English / Canada | [en-CA.json](../../integrations/alexa/en-CA.json) | memory timeline |
| English / Australia | [en-AU.json](../../integrations/alexa/en-AU.json) | memory timeline |
| English / India | [en-IN.json](../../integrations/alexa/en-IN.json) | memory timeline |

Both languages use invocation names containing at least two words. Verify recognition and model builds in the console and on devices for each locale. Publication also requires distribution regions and localized skill descriptions. A language edition must match the device language and account marketplace; selecting worldwide distribution does not add language support. See [Amazon's multilingual skill documentation](https://developer.amazon.com/en-US/docs/alexa/custom-skills/develop-skills-in-multiple-languages.html).

## Speaking to the skill

Say “Alexa, open Memory Timeline,” then:

- “Take a note, I want to go for a walk tomorrow.”
- “Read my replies.”
- “What's on my schedule?”
- “Stop.”

Japanese equivalents include “アレクサ、かけらノートを開いて” (open the skill), “メモして、明日は散歩に行きたい” (take a note), “返事を聞いて” (read replies), “予定を教えて” (read schedules) and “ストップ” (stop).

The Japanese note intent also includes both `かけらを投げて` and `欠片を投げて` sample utterances, with and without note text.

Prompts, errors and reprompts follow the Alexa request locale. English posts save `language: en`, and existing persona processing answers in the post's language. Saved replies, persona names and schedule text are not translated during read-aloud. Linking screens and settings follow the app or browser display language.

“Take a note” without content prompts for the text. Alexa reads the recognized text back and posts only after “Yes.” “No” or a correction lets you try again. Replying follows the same confirmation flow and targets the last persona reply read aloud. Stopping or cancelling before confirmation creates no post.

Launch and recognition-error prompts are short; longer instructions are reserved for Help. Alexa itself controls when the device stops listening.

Posts enter the normal timeline and personas respond asynchronously, so a reply may not exist immediately. Within the same voice session, Alexa reads the latest reply to the post just submitted. In a new session, it selects the newest persona reply among the most recent 100 conversations. You can reply to that answer. Read-aloud is limited to 700 characters per answer; use the app for the remainder or HTML infographics. There is no unread tracking or automatic announcement when a reply arrives.

Schedules are the next five saved app schedules, read in the account timezone. Missing, invalid or server-dependent `Local` timezones fall back to Japan time for Japanese and UTC for English, with the zone announced. Locale does not imply the user's timezone. US English reads month/day; other English locales read day/month, with year and AM/PM times. This does not query external calendars directly.

## Server configuration

Expose a Firebase-authenticated Web/combined server over HTTPS and set these variables. Alexa cannot be enabled in local unauthenticated mode.

| Environment variable | Value |
| --- | --- |
| `KAKERATTA_ALEXA_SKILL_ID` | Console-issued `amzn1.ask.skill.…` |
| `KAKERATTA_ALEXA_CLIENT_ID` | Dedicated OAuth client ID, such as `kakeratta-alexa` |
| `KAKERATTA_ALEXA_CLIENT_SECRET` | A sufficiently long random secret, identical in server secret configuration and the console |
| `KAKERATTA_ALEXA_REDIRECT_URLS` | All console Redirect URLs, separated by whitespace; matching includes the exact query string |

Also configure Firebase authentication and `KAKERATTA_PUBLIC_URL`. Keep secrets out of source control and frontend assets. Existing Terraform/VM environment definitions do not include the Alexa-specific values; add them to the actual deployment's environment and secret injection.

## Amazon Developer Console

1. Create a Custom Skill with Japanese and the desired English locales, using your own HTTPS endpoint.
2. Import each locale's JSON from the table into its Interaction Model JSON Editor, save and build. Verify utterances and invocation names in the console.
3. Set the endpoint to `https://<public-host>/api/alexa`. Regional endpoints may use the same URL. Use a TLS certificate from an Amazon-trusted CA.
4. Enable Account Linking with Authorization Code Grant:
   - Authorization URI: `https://<public-host>/alexa`
   - Access Token URI: `https://<public-host>/api/alexa/token`
   - Client ID / Client Secret: the environment values above
   - Client Authentication Scheme: HTTP Basic (credentials in the request body are also supported)
   - Scope: `alexa`
   - Require account linking before enabling the skill.
5. Configure every regional Redirect URL shown by the console on the server. All locales share the OAuth client ID and secret.
6. Set Test to Development and start linking in the Alexa app signed into the same Amazon account. Sign into kakeratta with Google and press the Alexa linking consent button.

The linking page uses existing Firebase login with Google redirect authentication rather than a popup. Add the public host to Firebase's authorized domains. Production sets `authDomain` to `kakeratta.net`; Nginx proxies `/__/auth/` to `https://kakeratta.firebaseapp.com`. Add `https://kakeratta.net/__/auth/handler` to the Google OAuth Web client's authorized redirect URIs, retaining existing entries. A separate-domain `authDomain` can send users back to login when third-party storage is restricted. Verify the round trip in Alexa's embedded browser and on a device.

Publication separately requires skill listing information, a privacy policy and Amazon review. After deploying the legal pages, use `https://kakeratta.net/privacy/` for Privacy Policy URL and `https://kakeratta.net/terms/` for Terms of Use URL. Repository changes do not register or publish the skill on Amazon or deploy production.

## Disconnecting and verification

In kakeratta, use Settings → External integrations → Disconnect Alexa to invalidate access/refresh tokens and pending authorization codes. You can also disable the skill in Alexa. Each workspace has one Alexa connection; completing a new link invalidates previous credentials. Access tokens expire after one hour and refresh tokens 365 days after linking, requiring relinking afterward. Temporary judge accounts cannot link.

After deployment, verify on a real device: unlinked guidance, exactly one timeline post, persona replies, schedules in the account timezone, stopping, and denied data access after disconnecting. Do not record conversation contents or tokens in request logs.

## Specifications and local validation

- [Amazon: HTTPS skills, signature and timestamp validation](https://developer.amazon.com/en-US/docs/alexa/custom-skills/host-a-custom-skill-as-a-web-service.html)
- [Amazon: Account Linking Concepts](https://developer.amazon.com/docs/alexaplus/account-linking/account-linking-concepts.html)
- [Amazon: Japanese SearchQuery slot](https://developer.amazon.com/ja-JP/docs/alexa/custom-skills/slot-type-reference.html)
- [Amazon: Account linking troubleshooting](https://www.developer.amazon.com/docs/alexaplus/account-linking/troubleshooting-account-linking.html)

```sh
mise exec -- go test ./internal/alexa ./internal/server
mise exec -- node --test tests/*.test.js
mise exec -- npm --prefix frontend run build
```

Tests use local signing certificates and stores without Amazon credentials. They do not replace console model builds, Firebase/Google linking round trips or device checks.

## Review without a Google account

Use **Review access** on the linking screen with a pre-provisioned review email/password. OAuth parameters are retained and consent continues in the same browser. See [review account setup](judge-access.md#alexa-certification).

## Updating interaction models

For post confirmation, corrections and replies, paste the updated JSON into each target locale's JSON Editor and run Save Model / Build Model. Updating the server alone does not add Yes/No and reply intents or slot-elicitation dialogs.

The Japanese model registers the invocation name as `欠片ノート`, matching the transcription observed when saying “かけらノート”. The display name and spoken instructions can remain “かけらノート”; users do not need to enter kanji. Import the updated [ja-JP.json](../../integrations/alexa/ja-JP.json) in the Japanese locale and run Save Model / Build Model. In the Console's Japanese simulator, verify both `欠片ノートを開いて` and `かけらノートを開いて`, then repeat the spoken invocation on a device before resubmitting. This repository change alone does not update Amazon's model, and the user has confirmed Japanese invocation on a device. The user has also confirmed English device invocation with `memory timeline`. This confirms the tested device setup, not all five English locales; rebuild and test any additional target locales separately.

## Certification testing instructions

Templates are available in [English](../../integrations/alexa/testing-instructions-en.txt) and [Japanese](../../integrations/alexa/testing-instructions-ja.txt). Replace email/password placeholders with review credentials and paste them into the private testing-instructions field. They include Help, cancellation before confirmation and an explanation that automatic reminders are not used. See [submission checks and verification record](judge-access.md#submission-instructions-and-final-checks).
