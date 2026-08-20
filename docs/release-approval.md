# Release approval

Use this checklist before a public release. The checklist does not prove that a
release or package exists.

## Required approval

- [ ] The approved history cleanup is complete.
- [ ] The second `gitleaks` history scan passes.
- [ ] The second `gitleaks` directory scan passes.
- [ ] `klauern/index-01-hook` is public.
- [ ] `v*.*.*` tags are protected.
- [ ] The `public-release` environment has required reviewers.
- [ ] `PUBLIC_RELEASE_APPROVED` is exactly `true`.
- [ ] Actions workflow permissions allow write operations.
- [ ] GHCR package visibility can be set to public.
- [ ] The approved Go builder and BuildKit digests are recorded.
- [ ] No unapproved local preparation remote change occurred.

## Required evidence

```text
Commit: <40-character commit>
Tag: v<MAJOR>.<MINOR>.<PATCH>
Go builder: docker.io/library/golang:1.26.6@sha256:<approved digest>
BuildKit: docker.io/moby/buildkit@sha256:<approved digest>
History cleanup evidence: <link-or-id>
Second gitleaks evidence: <link-or-id>
Repository visibility: public
Tag protection evidence: <link-or-id>
Environment reviewer evidence: <link-or-id>
Actions permission evidence: <link-or-id>
GHCR visibility evidence: <link-or-id>
Approval variable owner: <maintainer>
Approval time: <UTC timestamp>
Workflow run: <URL>
Release URL: <URL>
Published image digest: sha256:<digest>
checksums.txt digest: sha256:<digest>
Source SBOM digest: sha256:<digest>
Signature evidence: <link-or-id>
Attestation evidence: <link-or-id>
```

## Stop conditions

Stop the workflow when any condition occurs:

- The repository is not public.
- The approval variable is not exactly `true`.
- The tag is not annotated or does not match the commit.
- The commit is not an ancestor of `origin/main`.
- The artifact set, checksum, SBOM, signature, or attestation check fails.
- The GHCR package remains private.
- Anonymous digest inspection fails.
- The draft release does not contain exactly fourteen assets.

The first GHCR package push can be private. The workflow stops before it creates the
version image tag or GitHub Release. Set the package visibility to public, then rerun
the same tag workflow.

## Rollback record

Do not create a replacement tag. Keep the immutable digest for investigation. Remove
the version tag only through an approved registry operation. Correct the draft or
published Release through GitHub. Record every action and retain no private logs in
workflow artifacts.
