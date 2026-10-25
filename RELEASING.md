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
- Protect `v*.*.*` tags. Require maintainer review for tag creation.
- Configure the `public-release` environment with required reviewers.
- Set repository variable `PUBLIC_RELEASE_APPROVED` to `true` for this release.
- Set Actions workflow permissions to read and write.
- Confirm that the GHCR package can have public visibility.
- Record the approved Go builder and BuildKit image digests.
- Keep no provider or cluster credentials in the workflow.
- Do not add or change the local preparation repository remote without approval.

The release commit must be an ancestor of `origin/main`. The commit must match the
annotated tag object. GitHub must report the tag ref as protected. The tag must use the form `vMAJOR.MINOR.PATCH`.

## Exact sequence

1. Complete history cleanup and obtain maintainer approval.
2. Run both `gitleaks` scans again after cleanup.

   ```sh
   gitleaks git --redact --no-banner --exit-code 1 --log-opts="--all" .
   gitleaks dir --redact --no-banner --exit-code 1 .
   ```

3. Confirm repository, tag protection, environment reviewers, Actions permissions,
   and GHCR settings.
4. Set `PUBLIC_RELEASE_APPROVED=true`.
5. Create and push one annotated `vMAJOR.MINOR.PATCH` tag through the protected-tag
   process.
6. Review the validation, build, artifact, SBOM, signature, and attestation steps.
7. If the first package push is private, stop. The workflow must stop before it
   creates the version image tag or the GitHub Release. Set the package visibility to
   public, then rerun the same tag workflow.
8. Record the immutable image digest and GitHub Release asset list.
9. Run the verification commands below from a clean operator workstation.
10. Remove or rotate the temporary approval variable after the release.

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
ignores earlier merges. Do not set these variables as part of this implementation.
The initial rollout is not authorized here.
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

Drafting and publishing require separate owner-controlled settings.
`RELEASE_DRAFTER_PUBLISH=true` enables dispatch, and
`PUBLIC_RELEASE_APPROVED=true` confirms approval. The dispatch job also uses the
`public-release` environment, which must require reviewers. The tag release
workflow checks approval and protected-tag status. Keep `v*.*.*` tag protection
and required review. Do not bypass protection or add a token.

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
Protected tags: v*.*.*; required review confirmed
public-release reviewers: <maintainer list>
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
