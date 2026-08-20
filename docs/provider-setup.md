# Provider setup

This guide configures DeepSeek, TickTick, and Index 01 for one operator.
Use the official pages below when a provider changes its flow, price, or policy.

## Official sources

### DeepSeek

- [DeepSeek API documentation](https://api-docs.deepseek.com/)
- [DeepSeek Responses API](https://api-docs.deepseek.com/guides/responses_api)
- [DeepSeek models and pricing](https://api-docs.deepseek.com/quick_start/pricing)
- [DeepSeek rate limits](https://api-docs.deepseek.com/quick_start/rate_limit)
- [DeepSeek error codes](https://api-docs.deepseek.com/quick_start/error_codes)
- [DeepSeek privacy policy](https://cdn.deepseek.com/policies/en-US/deepseek-privacy-policy.html)

### TickTick

- [TickTick Open API documentation](https://developer.ticktick.com/docs/index.html#/openapi)
- [TickTick Open API source markdown](https://developer.ticktick.com/docs/openapi.md)
- [TickTick developer portal](https://developer.ticktick.com/)
- [TickTick privacy policy](https://ticktick.com/about/privacy)
- [TickTick terms](https://ticktick.com/about/tos)

### Index 01

- [Index 01 getting started](https://help.repebble.com/en/articles/15434751-index-01-getting-started-guide)
- [Index 01 webhooks](https://help.repebble.com/en/articles/15724406-index-advanced-features-mcp-webhook)
- [Index 01 product and privacy description](https://repebble.com/index)

## 1. Configure DeepSeek

1. Create or access a DeepSeek account.
2. Open [DeepSeek API keys](https://platform.deepseek.com/api_keys).
3. Create one API key.
4. Store the key in a secret manager.
5. Set `INDEX01_DEEPSEEK_TOKEN` to the key.
6. Select a current provider model and set `INDEX01_DEEPSEEK_MODEL` to its safe
   identifier.
7. Set `INDEX01_TIME_ZONE` to the IANA time zone used for extracted dates.
8. Keep the API base URL at `https://api.deepseek.com`.
9. Review the [Responses API guide](https://api-docs.deepseek.com/guides/responses_api)
   before changing the request or model.

The repository validates the configured model identifier and sends that safe
identifier in the Responses API request. Select a current DeepSeek model that
supports the required Responses API and strict structured output. Confirm this
support in the [official Responses API guide](https://api-docs.deepseek.com/guides/responses_api)
and model documentation before deployment. The operator must repeat this check
when the provider changes its model list. This documentation was checked on
2026-08-19; provider documentation can change after that date.

DeepSeek charges by token. Prices can change. Review the [current DeepSeek pricing](https://api-docs.deepseek.com/quick_start/pricing)
before deployment. Review the [rate limits](https://api-docs.deepseek.com/quick_start/rate_limit)
and [error codes](https://api-docs.deepseek.com/quick_start/error_codes)
when you set alerts and retry policies.

Read the [DeepSeek privacy policy](https://cdn.deepseek.com/policies/en-US/deepseek-privacy-policy.html)
before sending personal or confidential transcription data.

## 2. Configure TickTick

### One-operator deployment: use a personal token

Use a personal TickTick API token when one operator controls the TickTick account.
This is the simplest flow. It does not require an OAuth application.

1. Sign in to the TickTick web app.
2. Open **Settings**.
3. Open **Account**.
4. Create or copy **API Token**.
5. Store the token in a secret manager.
6. Set `INDEX01_TICKTICK_TOKEN` to the token.
7. Use `ticktick-projects` to list safe project summaries.
8. Select one open writable `TASK` project, or use the reserved `inbox` value.
9. Select one open writable `NOTE` project for note delivery.
10. Set `INDEX01_TICKTICK_DEFAULT_PROJECT_ID` to the task project identifier or
    `inbox`.
11. Set `INDEX01_TICKTICK_NOTE_PROJECT_ID` to the note project identifier.
12. Set `INDEX01_TICKTICK_PROJECT_ALIASES` only for additional task routes.

Run project discovery with only the TickTick token. Load the token into the
current shell through an approved secret manager. Do not type the token into
shell history. Stop if the token is absent:

```sh
test -n "${INDEX01_TICKTICK_TOKEN:-}" || exit 1
```

If the local image exists, use Docker:

```sh
INDEX01_TICKTICK_TOKEN="$INDEX01_TICKTICK_TOKEN" docker run --rm \
  --env INDEX01_TICKTICK_TOKEN \
  index-01-hook:local ticktick-projects
```

If Go is installed, run the command from source instead:

```sh
INDEX01_TICKTICK_TOKEN="$INDEX01_TICKTICK_TOKEN" go run . ticktick-projects
```

The command calls `GET https://api.ticktick.com/open/v1/project`. It prints a
sorted JSON array with only `id`, `kind`, `closed`, and `writable`. It does not
print names, content, permission values, tokens, or provider response bodies.
It does not load SQLite or require DeepSeek, webhook, database, or routing
configuration.

TickTick project records use `TASK` or `NOTE` kinds. An omitted or `null`
permission means owner access and is writable. `write` is writable. `read`,
`comment`, malformed, and unknown permissions are not writable. The command
rejects invalid project identifiers, invalid kinds, duplicate identifiers, and
malformed responses.

The reserved `inbox` value is valid only as the default task destination. Inbox
does not need a provider project record. Do not use `inbox` for notes or aliases.

The reviewed official TickTick Open API documentation does not state an API
rate limit. Do not assume a limit. Monitor provider responses and follow current
TickTick guidance.

Read the [TickTick privacy policy](https://ticktick.com/about/privacy) and
[TickTick terms](https://ticktick.com/about/tos) before sending extracted data.

### Multi-user applications: use OAuth

Use OAuth only when an application authorizes other users. Do not use OAuth
merely because one operator needs a personal token.

1. Register an application through the [TickTick developer portal](https://developer.ticktick.com/).
2. Keep the client identifier and client secret in a secret manager.
3. Send the user to `https://ticktick.com/oauth/authorize`.
4. Request both scopes: `tasks:read` and `tasks:write`.
5. Exchange the authorization code at `https://ticktick.com/oauth/token`.
6. Store each user token securely.
7. Call `GET /open/v1/project` with that user's token.
8. Allow the user to select open writable `TASK` and `NOTE` projects.
9. Revoke access with `POST /oauth/revoke` when the user disconnects.

Keep `tasks:read` and `tasks:write` in the authorization request. The service's
single-operator command accepts `INDEX01_TICKTICK_TOKEN`; it does not implement
multi-user account storage or OAuth callbacks.

## Common setup errors

- A DeepSeek `401` response means the API key failed authentication.
- A DeepSeek `402` response means the account has insufficient balance.
- A DeepSeek `429`, `500`, or `503` response needs bounded retry and status review.
- An invalid DeepSeek model must support the Responses API and structured output.
- `INDEX01_TIME_ZONE` must be an available IANA time zone. Do not use `Local`.
- A TickTick `401` response means the access token failed authentication.
- A TickTick `403` response can mean missing scopes or project access.
- Startup rejects unavailable, closed, read-only, or wrong-kind projects.
- OAuth integrations must request both `tasks:read` and `tasks:write`.

Do not include tokens, project names, provider bodies, or private content in an
issue or support report.

## 3. Configure Index 01

1. Run the service behind an HTTPS reverse proxy.
2. Publish the public webhook path, such as `https://hook.example.com/webhook`.
3. In the main Index 01 settings, configure the one supported webhook URL.
4. Set the webhook `Authorization` header to `Bearer <INDEX01_WEBHOOK_TOKEN>`.
5. Set the same token in `INDEX01_WEBHOOK_TOKEN` on the receiver.
6. Set **Send** to **Transcription**.
7. Set **Trigger** to the required button combination, or **All**.
8. Save the Index 01 webhook settings.
9. Optionally send one synthetic test transcription.
10. Confirm an HTTP success response and one queued recording.

The optional transcription test calls DeepSeek and can create one or more
TickTick items. It can incur provider cost. Use synthetic text only, then remove
created TickTick items through the normal TickTick interface.

The official Index 01 settings use these names:

- **Send** selects **Recording**, **Transcription**, or **Both**.
- **Trigger** selects a button combination or **All**.

This service recommends **Send = Transcription**. Extraction requires
**Send = Transcription** or **Send = Both**. **Send = Recording** is audio-only
intake. Audio-only intake stores bounded metadata and queues no extraction.

### Official sender behavior

Index 01 sends an HTTPS `POST` request with `multipart/form-data`. The documented
fields are:

- `audio`: the recording when **Send** includes **Recording**.
- `transcription`: the transcription text when **Send** includes **Transcription**.
- `recordedAt`: milliseconds since the Unix epoch.
- `client`: `ring` for the Ring client.

Index 01 includes user-defined headers. Official retry behavior and payload size
limits were not documented on the reviewed page.

### Receiver behavior in this project

The receiver expects the standard `Authorization` header and the configured
bearer token. The receiver supports audio for compatibility, but it does not
retain uploaded audio as application data. The receiver validates and stores
bounded metadata. The receiver deduplicates equivalent payloads and creates one
extraction job only when transcription is present.

The receiver applies fixed multipart and complete-body limits documented in
the [HTTP API reference](api.md). A failed request is unconfirmed. Check receiver state before a
retry. Do not describe receiver limits or deduplication as Index 01 guarantees.
Do not assume an undocumented sender retry policy.

## Data, privacy, cost, and outages

The data path is:

1. Index 01 sends the selected recording and/or transcription to this receiver.
2. The receiver sends transcription text to DeepSeek for task and note extraction.
3. The receiver sends extracted task and note data to TickTick.
4. SQLite stores processing data and extracted content. Backups can contain it.

Use transcription-only delivery when audio is not required. Protect the webhook
token, provider tokens, database, and backups. Do not place secrets in logs,
command history, support reports, or source control.

DeepSeek token pricing is documented in the [current pricing page](https://api-docs.deepseek.com/quick_start/pricing).
The reviewed official TickTick Open API sources do not state an API price or rate
limit. Check current TickTick account and product terms before deployment.
Provider prices, quotas, policies, and availability can change.

DeepSeek or TickTick outages can delay extraction or delivery. The service cannot
repair a provider outage. Operators must monitor status and error responses,
keep retries bounded, review ambiguous deliveries, and avoid duplicate manual
retries. The operator owns provider accounts, billing, rate-limit planning,
privacy notices, consent, retention, deletion, legal compliance, backups,
restoration tests, and incident response.

Review provider privacy documents before each material data-flow change:
[DeepSeek privacy](https://cdn.deepseek.com/policies/en-US/deepseek-privacy-policy.html),
[TickTick privacy](https://ticktick.com/about/privacy), and
[TickTick terms](https://ticktick.com/about/tos).
