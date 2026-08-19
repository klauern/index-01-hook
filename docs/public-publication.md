# Public publication

The public repository starts from a fresh, sanitized history. It is a brand-new
repository whose single root commit contains only the output of
[`scripts/clean-public-source.sh`](../scripts/clean-public-source.sh). No commit
from the private repository is ever published.

## Why a fresh history

- Earlier private commits contain deployment values that must not become
  public. A selective rewrite would have to prove a negative across every
  historical tree; a fresh root does not.
- The private repository also carries auxiliary refs (`refs/cmux/last-turn/*`)
  and unreachable objects from local agent work. A fresh repository never
  copies them because nothing is cloned, fetched, or rewritten from the
  private repository — the candidate is built from an exported file tree only.
- The private repository is retained unchanged as the archive: its refs,
  including `refs/cmux/*`, and its unreachable objects stay available for
  audit. Do not delete refs or prune objects there.

## Commit-identity boundary

Every public commit SHA differs from every private commit SHA. The root commit
of the public repository records the boundary in its message:

```text
Source-Private-Commit: <private commit the export was cut from>
Source-Tree-State: clean
Cut-Date: <UTC timestamp>
```

Old history exists only in the private archive. References to private commit
SHAs in issues or notes cannot be resolved from the public repository; that is
intentional.

## Building the candidate

```sh
./scripts/publish-public-history.sh --sign <output-directory>
```

The script exports the sanitized tree, creates the single root commit on
`main`, and then verifies a clean clone of the candidate: `go vet ./...`,
`go test ./...`, and a private-value scan that fails closed on any hit. It
refuses to run against a source tree with uncommitted changes unless
`--dry-run` is given, and a dry-run candidate is never published.
[`scripts/publish-public-history_test.sh`](../scripts/publish-public-history_test.sh)
validates the procedure.

## Root-commit signature

The publication candidate's root commit must be created with `--sign` and the
maintainer's registered signing key, so the history boundary itself is
attested. Unsigned candidates are for local review only.

## Publishing (manual, owner-approved)

Publication is never automated and never performed by the script. After the
[release approval checklist](release-approval.md) passes, the owner pushes the
candidate — not the private repository — with exactly this refspec:

```sh
git -C <candidate> push <public-remote-url> refs/heads/main:refs/heads/main
```

Rules:

- `refs/heads/main:refs/heads/main` is the only approved refspec.
- Mirror pushes (`git push --mirror`) are forbidden. A mirror push publishes
  every ref of the repository it runs in and must never be used here.
- Never push to the public remote from the private archive repository.
- The publish script never adds remotes and never pushes; if it appears to,
  stop and audit it.
