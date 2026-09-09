# Configuration preflight

Run `index-01-hook validate-config` with the candidate receiver configuration before a deployment.
The command uses the same configuration and routing validation as receiver startup.
It checks local configuration, constructs provider clients, and reads TickTick routing metadata.
It makes no model calls or TickTick writes.
It does not open SQLite, apply migrations, or create database files.

Success prints `{"status":"ok"}` and exits with status zero.
Failure exits with a nonzero status and a sanitized error.
The command does not print tokens, project identifiers, alias names, or provider responses.
Model authentication is not verified because this check makes no model calls.

Run the command in a temporary Job with the candidate image and existing configuration Secret.
Do not mount the production database in this Job.
Use `imagePullPolicy: Always` and an immutable GHCR digest.
Permit the same required provider network access as the receiver.
If the Job fails, stop the deployment before replacing the receiver or dashboard.

Receiver startup also validates provider routing before opening the database.
A routing failure leaves the existing database unchanged.

The preflight does not replace the [release approval](release-approval.md) requirements.
After approval, use the existing release workflow to publish the GHCR image.
Deploy the same immutable digest to the receiver and dashboard.
Verify each running version, readiness, dashboard identity, and configured capture intervals.
Retain the current compatible image and database recovery copy until validation passes.
