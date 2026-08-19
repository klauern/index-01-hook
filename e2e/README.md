# Synthetic public experience test

Run the opt-in end-to-end test from the repository root:

```sh
./scripts/public-experience-e2e.sh
```

The test builds uniquely tagged application and provider-fixture images with
the pinned Go image.
The test pulls the pinned Caddy image. The test does not contact a real provider.

The fixture serves verified HTTPS for `api.deepseek.com` and
`api.ticktick.com`. The fixture accepts only fixed synthetic tokens and content.
The fixture records only the safe event names `deepseek` and `ticktick-task`.

The test uses internal provider and proxy networks. A separate published
network contains only Caddy and the random loopback port. The application does
not join the published network. The provider fixture receives both provider
aliases. The application receives a temporary certificate authority at
`/etc/ssl/certs/ca-certificates.crt`. Caddy serves `public.e2e.test` on a random
loopback port. The script resolves that name to loopback and verifies the
certificate with `curl --cacert`.

The test starts the fixture, application, and Caddy in that order. The test
checks webhook receipt, queue completion, provider metadata, task routing,
deduplication, provider-free receipt behavior, readiness authentication, route
allowlisting, response headers, and redacted operator output.

The script removes containers, networks, volumes, certificates, event files,
and temporary diagnostics on every exit. The test is not part of the normal
Taskfile test target.
