# Support

Read the [public support contract](docs/public-support-contract.md) before
requesting support. The contract defines supported releases, deployments, and
providers. Public GitHub issues become available after source publication.

## Defects

Report a reproducible defect in a GitHub issue. Include the latest supported
release version, deployment method, safe reproduction steps, and redacted
logs. Use synthetic input for the reproduction.

## Usage questions

Read the README and public documentation first. Ask a usage question in a
GitHub issue when the documentation does not answer it. Include the smallest
safe example that explains the question.

## Provider outages

Check the provider's service status and provider support channels for an
outage. The project cannot restore a DeepSeek, TickTick, or Index 01 outage.
Report a local integration defect only after the provider service is available.

## Security reports

Use the private process in [SECURITY.md](SECURITY.md). Do not report a
security concern in a public issue.

## Safe diagnostics

Safe diagnostic information includes the release version, deployment method,
operating system and architecture, command names without sensitive arguments,
UTC timestamps, stable status or reason codes, and redacted logs. Use synthetic
payloads and remove credentials, tokens, headers, transcription text, item
content, provider bodies, private URLs, and personal data.

## Unsupported requests

Support does not cover older releases, prereleases, modified images, custom
infrastructure, unsupported providers, provider outages, credential recovery,
data recovery without a usable backup, or live debugging with private data.
