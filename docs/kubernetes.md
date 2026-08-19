# Advanced Kubernetes deployment

Docker Compose is the primary deployment method. Kubernetes is an advanced option
for operators who need cluster management. Operators must understand Kubernetes,
storage, ingress, TLS, and network policy before using this guide.

This guide describes portable resources. It does not claim validation on a live
cluster or with a live provider.

## Prerequisites

Prepare these items before deployment:

- Kubernetes 1.31 or a compatible cluster with `apps/v1`, `networking.k8s.io/v1`,
  PersistentVolumeClaim, Secret, and Pod Security support.
- `kubectl` with access to the target cluster.
- Permission to manage the documented workloads, Secrets, and coordination Leases.
- An approved Ingress controller and its IngressClass.
- A certificate and key for the public host, plus a plan to renew them.
  This project does not assume cert-manager.
- A filesystem-backed `ReadWriteOnce` (RWO) volume.
- Storage that supports `fsGroup` ownership for UID and GID `65532`.
- `age` and an approved X25519 recipient for encrypted backups.
- `shasum` for checksum creation and verification.
- An immutable application image digest.
- The exact approved Kubernetes context.
- Provider accounts, tokens, and writable TickTick projects.

The namespace is always `index-01-hook`. The deployment uses one replica and the
`Recreate` strategy. Do not run another process against the same SQLite volume.

No public image exists until release automation publishes one. Use a synthetic
digest for render-only checks. Use a locally published registry digest for
cluster evaluation. Do not treat an evaluation image as a supported artifact.

## Storage

The PVC requests one `ReadWriteOnce` filesystem volume. The volume must permit the
application security context to use UID and GID `65532` through `fsGroup`.
Confirm this behavior with the storage provider before deployment.

If `KUBE_STORAGE_CLASS` is empty, Kubernetes selects the cluster default storage
class. This is the portable default. Confirm that the default class provides the
required filesystem, access mode, and ownership behavior.

Set `KUBE_STORAGE_CLASS` to an approved class when the cluster default is not
suitable or when the operator requires a named storage class. The named class must
support the same RWO and `fsGroup` requirements.

## Inputs and rendered resources

The renderer requires these inputs:

| Input | Meaning |
| --- | --- |
| `IMAGE_REF` | Immutable image reference with a digest. |
| `REGISTRY_ACCESS_MODE` | `public` or `private`. |
| `KUBE_INGRESS_HOST` | Fully qualified domain name for the Ingress. |
| `KUBE_INGRESS_CLASS` | Approved IngressClass name. |
| `KUBE_TLS_SECRET` | Pre-created TLS Secret name. |
| `KUBE_STORAGE_CLASS` | Optional storage class. Empty selects the cluster default. |

The renderer outputs exactly these resources:

- Namespace
- ServiceAccount
- NetworkPolicy resources
- PersistentVolumeClaim
- Service
- Ingress
- Deployment
- Maintenance Pod

The renderer does not output an application Secret, TLS Secret, or registry pull
Secret. Create those Secrets through the protected workflows below.

The portable Ingress has TLS and exact `/webhook` and `/readyz` paths. It has no
controller-specific annotations. Keep `/healthz` and `/statusz` private.

## Prepare protected Secrets

Set the target context and public routing values before creating Secrets:

```sh
export KUBE_CONTEXT='<approved-context>'
export KUBE_INGRESS_HOST='hook.example.org'
export KUBE_INGRESS_CLASS='standard'
export KUBE_TLS_SECRET='index-01-hook-tls'
```

Create the namespace before creating namespaced Secrets:

```sh
kubectl --context="$KUBE_CONTEXT" apply --validate=strict \
  -f deploy/kubernetes/namespace.yaml
```

### Application Secret

Start with the repository example. Store the completed file outside the
repository with mode `0600`:

```sh
umask 077
install -m 600 deploy/kubernetes/index-01-hook-secrets.env.example \
  /secure/path/index-01-hook-secrets.env
# Edit /secure/path/index-01-hook-secrets.env with protected tooling.
chmod 600 /secure/path/index-01-hook-secrets.env
```

Create or replace the application Secret without writing a Secret YAML file:

```sh
kubectl --context="$KUBE_CONTEXT" create secret generic index-01-hook-secrets \
  --namespace index-01-hook \
  --from-env-file=/secure/path/index-01-hook-secrets.env \
  --dry-run=client -o yaml |
kubectl --context="$KUBE_CONTEXT" apply --validate=strict -f -
```

Do not commit the completed environment file. Do not print its values. Do not
place tokens on command lines.

### TLS Secret

Create the certificate and private key as protected files. Use mode `0600` and
store both files outside the repository:

```sh
chmod 600 /secure/path/hook.example.org.crt /secure/path/hook.example.org.key
kubectl --context="$KUBE_CONTEXT" create secret tls "$KUBE_TLS_SECRET" \
  --namespace index-01-hook \
  --cert=/secure/path/hook.example.org.crt \
  --key=/secure/path/hook.example.org.key \
  --dry-run=client -o yaml |
kubectl --context="$KUBE_CONTEXT" apply --validate=strict -f -
```

The TLS Secret must exist before deployment. Verify the certificate covers the
selected `KUBE_INGRESS_HOST`. Protect the private key and do not put it in Git.

### Registry access

Public GHCR mode needs no pull Secret. The selected public image must be available
from the cluster nodes after release automation publishes it. This public pull
path is unavailable while the release status says publication is pending. Until
then, use synthetic digests only for rendering or use an approved evaluation
registry digest.

Private mode requires the existing Secret named
`index-01-hook-registry-pull`. Create a protected Docker configuration file with
your approved credential tool. Do not put a password on a command line:

```sh
chmod 600 /secure/path/dockerconfig.json
kubectl --context="$KUBE_CONTEXT" create secret generic \
  index-01-hook-registry-pull \
  --namespace index-01-hook \
  --type=kubernetes.io/dockerconfigjson \
  --from-file=.dockerconfigjson=/secure/path/dockerconfig.json \
  --dry-run=client -o yaml |
kubectl --context="$KUBE_CONTEXT" apply --validate=strict -f -
```

Use `REGISTRY_ACCESS_MODE=private` only when the image registry requires
authentication. The renderer adds this Secret reference to the application and
maintenance Pod. The renderer never creates or processes registry credentials.

## Render and validate

Set the image and registry values from the approved deployment record. Keep the
previous context and routing values. The image reference must remain immutable:

```sh
export IMAGE_REF='ghcr.io/klauern/index-01-hook@sha256:<image-digest>'
export REGISTRY_ACCESS_MODE='public'
unset KUBE_STORAGE_CLASS
```

Render with the cluster default storage class:

```sh
task render \
  IMAGE_REF="$IMAGE_REF" \
  REGISTRY_ACCESS_MODE="$REGISTRY_ACCESS_MODE" \
  KUBE_INGRESS_HOST="$KUBE_INGRESS_HOST" \
  KUBE_INGRESS_CLASS="$KUBE_INGRESS_CLASS" \
  KUBE_TLS_SECRET="$KUBE_TLS_SECRET" \
  KUBE_STORAGE_CLASS="${KUBE_STORAGE_CLASS:-}"
```

Render with an explicit approved storage class:

```sh
export KUBE_STORAGE_CLASS='<approved-rwo-storage-class>'
task render \
  IMAGE_REF="$IMAGE_REF" \
  REGISTRY_ACCESS_MODE="$REGISTRY_ACCESS_MODE" \
  KUBE_INGRESS_HOST="$KUBE_INGRESS_HOST" \
  KUBE_INGRESS_CLASS="$KUBE_INGRESS_CLASS" \
  KUBE_TLS_SECRET="$KUBE_TLS_SECRET" \
  KUBE_STORAGE_CLASS="$KUBE_STORAGE_CLASS"
```

The second command replaces the first output. Use only one storage mode for a
deployment. For a private registry, export `REGISTRY_ACCESS_MODE=private` after creating
the pull Secret. Pass the selected registry and storage values to each task.

Run client validation before any cluster mutation:

```sh
task dry-run \
  IMAGE_REF="$IMAGE_REF" \
  REGISTRY_ACCESS_MODE="$REGISTRY_ACCESS_MODE" \
  KUBE_INGRESS_HOST="$KUBE_INGRESS_HOST" \
  KUBE_INGRESS_CLASS="$KUBE_INGRESS_CLASS" \
  KUBE_TLS_SECRET="$KUBE_TLS_SECRET" \
  KUBE_STORAGE_CLASS="${KUBE_STORAGE_CLASS:-}" \
  KUBE_CONTEXT="$KUBE_CONTEXT"
```

The client dry-run validates each rendered resource and checks both workload image
fields. It does not contact DeepSeek, TickTick, an Ingress endpoint, or a public
provider.

After approval, run server validation against the exact context:

```sh
task server-dry-run \
  IMAGE_REF="$IMAGE_REF" \
  REGISTRY_ACCESS_MODE="$REGISTRY_ACCESS_MODE" \
  KUBE_INGRESS_HOST="$KUBE_INGRESS_HOST" \
  KUBE_INGRESS_CLASS="$KUBE_INGRESS_CLASS" \
  KUBE_TLS_SECRET="$KUBE_TLS_SECRET" \
  KUBE_STORAGE_CLASS="${KUBE_STORAGE_CLASS:-}" \
  KUBE_CONTEXT="$KUBE_CONTEXT"
```

Server dry-run contacts the Kubernetes API. It does not persist resources.

## Deploy and verify

Bootstrap the namespace before the task deploy. Namespace bootstrap is mandatory,
even when the namespace already exists. Create the protected Secrets after the
namespace exists:

```sh
kubectl --context="$KUBE_CONTEXT" apply --validate=strict \
  -f dist/manifests/namespace.yaml

task deploy \
  IMAGE_REF="$IMAGE_REF" \
  REGISTRY_ACCESS_MODE="$REGISTRY_ACCESS_MODE" \
  KUBE_INGRESS_HOST="$KUBE_INGRESS_HOST" \
  KUBE_INGRESS_CLASS="$KUBE_INGRESS_CLASS" \
  KUBE_TLS_SECRET="$KUBE_TLS_SECRET" \
  KUBE_STORAGE_CLASS="${KUBE_STORAGE_CLASS:-}" \
  KUBE_CONTEXT="$KUBE_CONTEXT"
```

The deploy task verifies that the namespace exists, then applies the rendered
resources idempotently. It waits for the Deployment rollout. The rendered
namespace resource is safe to apply after this required bootstrap.

Inspect resources and recent application logs:

```sh
task status KUBE_CONTEXT="$KUBE_CONTEXT"
task logs KUBE_CONTEXT="$KUBE_CONTEXT"
kubectl --context="$KUBE_CONTEXT" rollout status \
  deployment/index-01-hook -n index-01-hook --timeout=180s
```

Verify the external ready endpoint over HTTPS. Load the token from a protected
environment or secret manager. Do not paste the token into shell history:

```sh
curl --fail-with-body --silent --show-error \
  --header "Authorization: Bearer ${INDEX01_WEBHOOK_TOKEN}" \
  "https://${KUBE_INGRESS_HOST}/readyz"
```

The endpoint must return `200` for a healthy worker. A `503` response can report
degraded worker, queue, or provider state. Provider `last_failed` alone does not
necessarily cause `503`; use the [HTTP API reference](api.md) for the aggregate
readiness rules. Do not publish `/healthz` or `/statusz`.

## Ingress and network policy

The Ingress only declares exact `/webhook` and `/readyz` paths. Configure the
approved Ingress controller with a 64 MiB maximum request body and a per-client
rate limit of 10 webhook requests per minute with a burst of 20. Configure the
controller to allow only `POST /webhook` and `GET /readyz`. These controls are
controller-specific and are not encoded as portable Ingress annotations. If the
controller cannot provide these controls, place an approved rate-limiting and
body-limiting edge before the Ingress. Review the effective settings before
deployment.

The baseline NetworkPolicy default-denies ingress and egress for all pods in the
namespace. It allows application ingress on TCP port `8080` from any source that
the cluster network can route to the Pod. Standard NetworkPolicy cannot inspect
HTTP methods or URL paths, so this rule cannot distinguish public routes from
private routes. The application still requires the webhook Bearer token for
`POST /webhook` and `GET /readyz`; `/healthz` and `/statusz` remain unauthenticated
and must be treated as reachable by any source that can reach port `8080`. The
portable baseline does not provide shared-cluster isolation. Use a dedicated
or trusted cluster, or add an approved policy or proxy, before deployment to a
shared cluster.

The selected egress control for the supported deployment is the portable
`index-01-hook-provider-egress` NetworkPolicy. It allows application DNS
egress on UDP and TCP port `53`. HTTPS egress is limited to TCP port `443`
toward public IPv4 and IPv6 ranges
and excludes private, loopback, link-local, carrier-grade NAT, and multicast
ranges, so the application cannot reach cluster-local or other private
addresses through the approved policy. The maintenance Pod remains
default-denied.

Standard NetworkPolicy cannot restrict TCP `443` by DNS name, so provider-only
(FQDN) egress is not possible with this portable policy. Public HTTPS
destinations other than DeepSeek and TickTick remain reachable. This
broad-egress limitation is accepted for the portable baseline and requires
explicit operator approval before production use. Operators who require
provider-only egress must use an approved fail-closed egress proxy or a
CNI-specific FQDN policy (for example, Cilium). DNS egress remains broad
because standard NetworkPolicy cannot select a portable cluster DNS service.
Document and test the selected control. Do not add an unreviewed
private-network exception.

Verify the Ingress controller and DNS labels before deployment. The portable policy
does not assume controller-specific namespace or pod labels. If the cluster needs
more specific ingress policy, review the policy with the cluster operator.

## Encrypted backup

Install `age` and use an approved recipient. Export through the task so the live
SQLite backup is streamed directly into authenticated encryption:

```sh
task backup-export \
  DESTINATION=/secure/path/index01-backup-20260812T120000Z.db.age \
  AGE_RECIPIENT=age1... \
  KUBE_CONTEXT="$KUBE_CONTEXT"
```

The task acquires the maintenance Lease and requires one ready application.
It writes no plaintext host backup. It publishes the encrypted artifact and its
SHA-256 checksum without replacement.
Verify the checksum before any restore:

```sh
(
  cd /secure/path
  shasum -a 256 -c index01-backup-20260812T120000Z.db.age.sha256
)
```

Keep the `age` identity outside the cluster in an approved secret manager. Keep
the encrypted artifact and checksum together. Do not store the only backup on the
production PVC.

## Complete restore sequence

A restore replaces the database. Obtain approval, verify the backup checksum, and
use the exact image digest that the maintenance Pod must run. Require exclusive
cluster access for the full operation. Raw `kubectl` mutations can bypass the
maintenance lock guard.

The restore script acquires `index-01-hook-maintenance-lock` before workload
changes. Supported deploy, restart, rollback, and first-deploy withdrawal tasks
acquire the same Lease with an atomic compare-and-set operation. Each task holds
the Lease until the operation ends. The persistent Lease uses `released` when it
is available. Raw `kubectl` commands can bypass this control. Use exclusive operator access
for every raw `kubectl` mutation.

The restore script requires seven arguments: backup, checksum, age identity,
context, namespace, expected image digest, and the absolute rendered maintenance
Pod path.

Render the exact maintenance image. Do not apply the maintenance Pod manually:

```sh
task render \
  IMAGE_REF="$IMAGE_REF" \
  REGISTRY_ACCESS_MODE="$REGISTRY_ACCESS_MODE" \
  KUBE_INGRESS_HOST="$KUBE_INGRESS_HOST" \
  KUBE_INGRESS_CLASS="$KUBE_INGRESS_CLASS" \
  KUBE_TLS_SECRET="$KUBE_TLS_SECRET" \
  KUBE_STORAGE_CLASS="${KUBE_STORAGE_CLASS:-}"
maintenance_manifest="$(pwd)/dist/manifests/maintenance-pod.yaml"
```

Call the restore script. The script owns the complete protected lifecycle:

```sh
./scripts/restore-external-backup.sh \
  /secure/path/index01-backup-20260812T120000Z.db.age \
  /secure/path/index01-backup-20260812T120000Z.db.age.sha256 \
  /secure/path/index01-backup-age-identity.txt \
  "$KUBE_CONTEXT" \
  index-01-hook \
  "$IMAGE_REF" \
  "$maintenance_manifest"
```

The script acquires the Lease before it changes the workload. The backup and rendered-manifest directories must permit protected hard links
for race-resistant snapshotting. The script copies and verifies those encrypted snapshots, scales
the Deployment to zero, and waits for
all application Pods to stop. The script validates that the manifest contains
only the named maintenance Pod in the fixed namespace. It replaces the
maintenance Pod from that manifest. It verifies the Pod image, command, database
path, writable
volume mount, Pod UID, PVC name, and PVC UID.

The script decrypts the complete backup to a protected temporary file. It rechecks
the maintenance Pod, PVC, Lease, and application Pod state before restore. After a
successful restore, it deletes the maintenance Pod, scales the Deployment to one,
waits for rollout health, and then releases the Lease.

Verify the external ready endpoint after the script succeeds:

```sh
curl --fail-with-body --silent --show-error \
  --header "Authorization: Bearer ${INDEX01_WEBHOOK_TOKEN}" \
  "https://${KUBE_INGRESS_HOST}/readyz"
```

If restore fails, stop. The script scales the Deployment back to zero when
workload shutdown started. It leaves the Lease in place and does not leave an
unhealthy or unverified workload running. A host crash or `SIGKILL` can leave protected
temporary files under `${TMPDIR:-/tmp}`. Review and remove stale
`index01-restore.*` directories through an approved host recovery action.

Treat a retained Lease as active. Recover it only after confirming that no restore
process runs, no maintenance Pod operation runs, and no application Pod runs.
Read its holder identity, review that exact value, and release it atomically:

```sh
stale_holder=$(kubectl --context="$KUBE_CONTEXT" get \
  lease/index-01-hook-maintenance-lock -n index-01-hook \
  -o jsonpath='{.spec.holderIdentity}')
./scripts/kubernetes-maintenance-lock.sh release \
  "$KUBE_CONTEXT" index-01-hook "$stale_holder"
```

The release uses a server-side compare-and-set operation. It cannot release a
Lease whose holder changed after inspection.

## Upgrade, rollback, and rotation

Use this sequence for an immutable-digest upgrade:

1. Export and verify an encrypted backup.
2. Record the current image digest, Deployment revision, and pod template.
3. Render the new immutable image digest.
4. Run client and approved server dry-runs.
5. Deploy the new digest after approval.
6. Verify rollout, `/readyz`, private status, logs, and queue state.
7. Keep the old digest and verified backup until verification completes.

For an image rollback, verify the approved prior Deployment revision and its exact
immutable image digest. Use the guarded task:

```sh
task rollback REVISION=7 \
  CONFIRM=rollback-to-revision-7 \
  KUBE_CONTEXT="$KUBE_CONTEXT"
```

Verify rollout and health after rollback. The rollback changes the Deployment only;
it does not replace the PVC.

If a schema change prevents an image rollback, stop the application, verify the
backup checksum, and use the complete restore sequence. A schema rollback requires
a compatible backup. Do not start the application before restore succeeds.

For secret rotation, update the protected environment file, reapply the application
Secret, and restart only after approval:

```sh
kubectl --context="$KUBE_CONTEXT" create secret generic index-01-hook-secrets \
  --namespace index-01-hook \
  --from-env-file=/secure/path/index-01-hook-secrets.env \
  --dry-run=client -o yaml |
kubectl --context="$KUBE_CONTEXT" apply --validate=strict -f -
task restart KUBE_CONTEXT="$KUBE_CONTEXT"
kubectl --context="$KUBE_CONTEXT" rollout status deployment/index-01-hook \
  -n index-01-hook --timeout=180s
```

Rotate the sender token and receiver token in one controlled window. Never print
secret values.

## Safe removal

Removal is destructive. Removing the PVC destroys the SQLite database. Keep PVC
removal separate from application removal, and verify a usable encrypted backup
before any destructive action.

Acquire the maintenance Lease before application removal. Keep the Lease if any
command fails. Release it only after all application Pods are gone:

```sh
set -eu
removal_holder="removal-$(date -u +%s)-$$"
./scripts/kubernetes-maintenance-lock.sh acquire \
  "$KUBE_CONTEXT" index-01-hook "$removal_holder"
kubectl --context="$KUBE_CONTEXT" delete ingress/index-01-hook \
  service/index-01-hook -n index-01-hook
kubectl --context="$KUBE_CONTEXT" scale deployment/index-01-hook \
  --replicas=0 -n index-01-hook
application_pods=$(kubectl --context="$KUBE_CONTEXT" get pods \
  -l app.kubernetes.io/name=index-01-hook -n index-01-hook -o name)
if [ -n "$application_pods" ]; then
  kubectl --context="$KUBE_CONTEXT" wait --for=delete pod \
    -l app.kubernetes.io/name=index-01-hook -n index-01-hook --timeout=180s
fi
kubectl --context="$KUBE_CONTEXT" delete deployment/index-01-hook \
  -n index-01-hook --wait=true
./scripts/kubernetes-maintenance-lock.sh release \
  "$KUBE_CONTEXT" index-01-hook "$removal_holder"
```

Delete the PVC only after separate approval and backup verification:

```sh
kubectl --context="$KUBE_CONTEXT" delete pvc/index-01-hook-data \
  -n index-01-hook
```

Delete the namespace only after reviewing every remaining resource. Namespace
removal also destroys remaining namespaced resources, including any PVC:

```sh
kubectl --context="$KUBE_CONTEXT" delete namespace index-01-hook
```

Keep live cluster commands outside normal contributor tests. This guide does not
claim cluster validation, provider validation, or public image publication.
