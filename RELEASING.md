# Releasing

This guide describes the gated public release process. The workflow pushes an
untagged image digest, signatures, and attestations before it publishes a GitHub
Release. These staging objects are not a supported release. A later failure can
leave them in GHCR for investigation.

## Preconditions

Complete every precondition before you push a tag:

- Complete the approved history cleanup.
- Run the second `gitleaks` history and directory scan after cleanup.
- Confirm that `klauern/index-01-hook` is public.
- Protect existing `v*.*.*` tags against updates, deletion, and non-fast-forward changes.
- Keep the `public-release` environment. Normal releases do not require a separate environment approval.
- Enable `PUBLIC_RELEASE_APPROVED` and `RELEASE_DRAFTER_PUBLISH` only after the owner approves automatic publication.
- Set Actions workflow permissions to read and write.
- Confirm that the GHCR package can have public visibility.
- Record the approved Go builder and BuildKit image digests.
- Keep no provider or cluster credentials in the workflow.
- Do not add or change the local preparation repository remote without approval.

The release commit must be an ancestor of `origin/main`. The commit must match the
annotated tag object. GitHub must report the tag ref as protected. The tag must use the form `vMAJOR.MINOR.PATCH`.

## Normal release sequence

1. Complete the preconditions and owner-approved automation setup.
2. Add `release:major`, `release:minor`, or `release:patch` to the pull request.
3. Review and merge the pull request.
4. Wait for CI to pass on the exact `main` merge commit.
5. The workflow freezes the candidate, creates its protected annotated tag, and dispatches the release workflow.
6. The release workflow validates, builds, signs, verifies, and publishes the release.
7. Record the immutable image digest and GitHub Release asset list.
8. Run the verification commands below from a clean operator workstation.

PR review and merge provide release approval. No separate environment approval is required.
Required checks, tag protection, and artifact verification remain in effect.

For owner-approved manual tag recovery, run both history and directory scans first:

```sh
gitleaks git --redact --no-banner --exit-code 1 --log-opts="--all" .
gitleaks dir --redact --no-banner --exit-code 1 .
```

If the first package push is private, stop before creating the version image tag or publishing the GitHub Release.
Correct package visibility through an owner-approved operation. Retry only the same validated candidate.
Keep failed staging objects for investigation. Do not replace an existing version tag.

The workflow creates only the exact image version tag after anonymous digest
inspection. It does not create `latest`, `stable`, `main`, major, minor, or commit
tags. For manual tags, the workflow creates a draft only after all release checks pass.

## Automatic releases from merged pull requests

The Release Drafter workflow can prepare one release candidate from merged pull
requests on `main`. The workflow does not enable itself. An owner must configure
the repository variables and release environment.

Set `RELEASE_DRAFTER_ENABLED=true` to enable candidate drafting. Set
`RELEASE_DRAFTER_BASELINE` to the exact 40-character commit SHA on the first-parent
history of `main`. Set it to the deployment or enablement commit. The workflow
ignores earlier merges. Configure these variables only during owner-approved setup.
Keep the baseline unchanged after the first candidate is created.

The workflow checks successful CI for the exact merge commit. It examines merged
`main` history in order and selects the oldest unreleased pull request. Source
history is the durable queue. A missing or canceled CI run blocks that candidate.
An owner must rerun CI for its exact commit. The workflow does not skip it.

Release Drafter v7.9.0 is pinned to a verified action SHA. It builds notes from
the selected merge's first-parent range and uses that merge as the target commit.
The workflow freezes the draft's notes, labels, baseline, base version, pull
request, and commit. Later merges cannot change that candidate. Only one candidate
draft may exist. A matching draft is adopted. An unrelated draft or partial upload
stops processing; the workflow does not overwrite or remove it.

The default version increment is patch. Use exactly one of `release:major`,
`release:minor`, or optional `release:patch` to select an increment. Conflicting
release labels stop processing. Feature and change categories only group notes;
they do not select a version.
Create these labels during owner-approved setup. To block conflicting labels before merge,
make `Check release label conflicts` a required check. This implementation does not change branch protection.

Drafting and publishing use persistent owner-controlled settings.
`RELEASE_DRAFTER_ENABLED=true` enables drafting. `RELEASE_DRAFTER_PUBLISH=true` enables dispatch.
`PUBLIC_RELEASE_APPROVED=true` records owner approval for automatic publication from trusted `main` merges.
These variables are enable switches, not per-release approval steps.
Set either publication variable to `false` to stop publication after a failure.
The `public-release` environment remains in use without a required-reviewer rule.
The tag release workflow checks publication approval and protected-tag status.
The owner-approved tag rules permit new version tags from repository writers and the workflow token.
Existing version tags remain protected against updates, deletion, and non-fast-forward changes.
An administrator-only creation rule blocks the workflow token. Keep creation permission separate from existing-tag protection.
Obtain owner approval before changing these permissions or adding credentials.

After validation, the workflow creates an annotated protected version tag and
dispatches the release workflow with `GITHUB_TOKEN`. The dispatch uses the tag
name as its ref, so the release workflow keeps the tag-ref identity. The tag
release workflow retains the existing fourteen assets, signatures, and
attestations.

If tag dispatch fails after tag creation, retain the tag. An owner may inspect
the candidate and approve recovery. Rerun the Release Drafter workflow to dispatch
the same immutable candidate. Do not overwrite a draft, replace a tag, or retry a
partial asset upload without owner approval. If an image version was pushed before
a later failure, stop and inspect it; do not clobber it.

Repository variables, environment reviewers, and protection settings remain owner-only changes. This guide does not authorize live rollout.

## Verification

Set values from the workflow summary and release page:

```sh
export VERSION='v0.1.0'
export IMAGE='ghcr.io/klauern/index-01-hook'
export DIGEST='sha256:replace-with-the-verified-digest'
export RELEASE_COMMIT='replace-with-the-verified-commit'
```

Verify the image, its digest, and its signatures:

```sh
docker buildx imagetools inspect "$IMAGE@$DIGEST"
docker buildx imagetools inspect "$IMAGE:$VERSION"
docker pull "$IMAGE@$DIGEST"
docker image inspect "$IMAGE@$DIGEST" >/dev/null
cosign verify "$IMAGE@$DIGEST" \
  --certificate-identity "https://github.com/klauern/index-01-hook/.github/workflows/release.yml@refs/tags/$VERSION" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
gh attestation verify oci://"$IMAGE@$DIGEST" \
  --repo klauern/index-01-hook
```

Verify each downloaded release asset. Use the GitHub Release asset directory:

```sh
cd release-assets
sha256sum --check checksums.txt
jq -e '.spdxVersion and (.packages | type == "array")' \
  index-01-hook-source.spdx.json >/dev/null
chmod 0755 \
  "index-01-hook_${VERSION}_darwin_amd64" \
  "index-01-hook_${VERSION}_darwin_arm64" \
  "index-01-hook_${VERSION}_linux_amd64" \
  "index-01-hook_${VERSION}_linux_arm64"
for bundle in *.sigstore.json; do
  test -s "$bundle"
done
for file in \
  THIRD_PARTY_NOTICES.txt \
  checksums.txt \
  "index-01-hook_${VERSION}_darwin_amd64" \
  "index-01-hook_${VERSION}_darwin_arm64" \
  "index-01-hook_${VERSION}_linux_amd64" \
  "index-01-hook_${VERSION}_linux_arm64" \
  index-01-hook-source.spdx.json; do
  cosign verify-blob --bundle "$file.sigstore.json" "$file" \
    --certificate-identity "https://github.com/klauern/index-01-hook/.github/workflows/release.yml@refs/tags/$VERSION" \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com
done
```

Verify the GitHub attestations for release files:

```sh
gh attestation verify checksums.txt --repo klauern/index-01-hook
for file in \
  THIRD_PARTY_NOTICES.txt \
  "index-01-hook_${VERSION}_darwin_amd64" \
  "index-01-hook_${VERSION}_darwin_arm64" \
  "index-01-hook_${VERSION}_linux_amd64" \
  "index-01-hook_${VERSION}_linux_arm64" \
  index-01-hook-source.spdx.json; do
  gh attestation verify "$file" --repo klauern/index-01-hook
done
```

Check that the GitHub Release has exactly fourteen assets. Confirm that it
contains `THIRD_PARTY_NOTICES.txt` and its Sigstore bundle. Confirm that the release
is not a draft and that the tag points to `$RELEASE_COMMIT`.

## Sanitized approval record

Use this record in the maintainer approval system. Do not include tokens, private
URLs, provider data, cluster data, or personal data.

```text
Release approval: index-01-hook
Commit: <40-character commit>
Tag: v<MAJOR>.<MINOR>.<PATCH>
Approved Go builder: docker.io/library/golang:1.26.10@sha256:<digest>
Approved BuildKit: docker.io/moby/buildkit@sha256:<digest>
History cleanup: approved by <maintainer>; evidence <link-or-id>
Second gitleaks scan: pass; evidence <link-or-id>
Repository visibility: public; evidence <link-or-id>
GHCR package visibility: public or first-push stop confirmed
Protected tags: v*.*.*; approved automation identity confirmed
Release approval: reviewed and merged pull request; no separate environment review
PUBLIC_RELEASE_APPROVED: true; set by <maintainer>
Actions write permission: confirmed
Workflow run: <URL>
Release URL: <URL>
Published image digest: sha256:<digest>
checksums.txt digest: sha256:<digest>
Source SBOM digest: sha256:<digest>
Signature and attestation evidence: <link-or-id>
Stop conditions: failed gate, changed commit, unexpected asset, private package,
  failed signature, failed attestation, or failed anonymous digest check
Rollback: do not publish a replacement tag; retain the immutable digest, remove
  the version tag if required, and document the GitHub Release correction
Approver: <maintainer>
Approved at: <UTC timestamp>
```

If a stop condition occurs, do not bypass the workflow gate. Preserve the logs needed
for diagnosis without uploading private logs. Correct the cause and rerun the same
validated tag only when the release owner approves the rerun. If a draft Release
already exists, stop and resolve or remove that draft through a separate approved
GitHub operation before rerunning.
