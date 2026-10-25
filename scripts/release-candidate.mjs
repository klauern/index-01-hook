import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import { writeFileSync } from "node:fs";

export const REPOSITORY = "klauern/index-01-hook";
const MARKER = "<!-- release-candidate:";
const VERSION_RE = /^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/;

/** Check the version selected by Release Drafter against the frozen labels. */
export function expectedVersion(previous, labels) {
  if (!VERSION_RE.test(previous ?? "")) throw new Error("Invalid previous release version");
  let [major, minor, patch] = previous.slice(1).split(".").map(BigInt);
  const [label] = releaseLabels(labels);
  if (label === "release:major") { major += 1n; minor = 0n; patch = 0n; }
  else if (label === "release:minor") { minor += 1n; patch = 0n; }
  else patch += 1n;
  return `v${major}.${minor}.${patch}`;
}

/** Select the highest published stable version as the increment base. */
function previousVersion(releases) {
  const versions = releases.filter((r) => !r.draft && !r.prerelease && VERSION_RE.test(r.tag_name)).map((r) => r.tag_name);
  versions.sort((a, b) => {
    const left = a.slice(1).split(".").map(BigInt);
    const right = b.slice(1).split(".").map(BigInt);
    for (let i = 0; i < 3; i++) if (left[i] !== right[i]) return left[i] > right[i] ? -1 : 1;
    return 0;
  });
  return versions[0] ?? "v0.0.0";
}
/** Require a full commit SHA and normalize its letter case. */
export function validBaseline(value) {
  if (!/^[a-f0-9]{40}$/i.test(value ?? "")) throw new Error("RELEASE_BASELINE must be an exact 40-character commit SHA");
  return value.toLowerCase();
}
/** Reject malformed labels and conflicting release increments. */
export function releaseLabels(labels) {
  if (!Array.isArray(labels)) throw new Error("Malformed pull request labels");
  const selected = labels.map((label) => typeof label === "string" ? label : label?.name);
  if (selected.some((label) => typeof label !== "string")) throw new Error("Malformed pull request labels");
  const release = selected.filter((label) => ["release:major", "release:minor", "release:patch"].includes(label));
  if (release.length > 1) throw new Error("Conflicting release labels");
  return release;
}
/** Compute the SHA-256 digest of the release notes. */
export function digest(value) { return createHash("sha256").update(value, "utf8").digest("hex"); }
/** Record the version decision and the exact release notes. */
export function makeManifest({ baseline, commit, pr, version, notes, labels, baseVersion }) {
  baseline = validBaseline(baseline);
  if (!/^[a-f0-9]{40}$/i.test(commit ?? "")) throw new Error("Invalid candidate commit");
  if (!Number.isSafeInteger(Number(pr)) || Number(pr) < 1) throw new Error("Invalid candidate pull request");
  if (!VERSION_RE.test(version ?? "")) throw new Error("Invalid release version");
  if (!Array.isArray(labels) || labels.some((x) => typeof (typeof x === "string" ? x : x?.name) !== "string" || (typeof x === "string" ? x : x.name).length === 0)) throw new Error("Malformed manifest labels");
  if (expectedVersion(baseVersion, labels) !== version) throw new Error("Release Drafter version does not match the candidate labels");
  return { schema: 1, baseline, commit: commit.toLowerCase(), pr: Number(pr), version, base_version: baseVersion, notes_sha256: digest(String(notes).trimEnd()), labels: labels.map((x) => typeof x === "string" ? x : x.name).sort() };
}
/** Validate one frozen manifest and verify its release-note digest. */
export function parseManifest(body) {
  const matches = [...String(body).matchAll(/<!-- release-candidate:([\s\S]*?) -->/g)];
  if (matches.length !== 1) throw new Error("Draft must contain exactly one release candidate manifest");
  let m;
  try { m = JSON.parse(matches[0][1]); } catch { throw new Error("Malformed release candidate manifest"); }
  if (!m || m.schema !== 1 || !/^[a-f0-9]{40}$/.test(m.baseline ?? "") || !/^[a-f0-9]{40}$/.test(m.commit ?? "") || !Number.isSafeInteger(m.pr) || m.pr < 1 || !VERSION_RE.test(m.version ?? "") || !/^[a-f0-9]{64}$/.test(m.notes_sha256 ?? "") || !Array.isArray(m.labels) || m.labels.some((x) => typeof x !== "string") || JSON.stringify(m.labels) !== JSON.stringify([...m.labels].sort())) throw new Error("Invalid release candidate manifest");
  if (expectedVersion(m.base_version, m.labels) !== m.version) throw new Error("Frozen version does not match the candidate labels");
  const notes = String(body).replace(matches[0][0], "").trimEnd();
  if (digest(notes) !== m.notes_sha256) throw new Error("Draft release notes changed after freeze");
  return m;
}
/** Freeze a draft once, and reject changes on a repeated call. */
export function freezeDraft(draft, { baseline, commit, pr, labels, baseVersion }) {
  if (!draft || draft.draft !== true || draft.prerelease !== false) throw new Error("Release Drafter did not create a stable draft");
  if (draft.target_commitish !== commit) throw new Error("Draft target commit does not match candidate");
  if ((draft.assets ?? []).length) throw new Error("Candidate draft must not contain assets");
  const existing = String(draft.body ?? "");
  const previous = [...existing.matchAll(/<!-- release-candidate:[\s\S]*? -->/g)];
  if (previous.length) {
    const parsed = parseManifest(existing);
    const expected = makeManifest({ baseline, commit, pr, version: draft.tag_name, notes: existing.replace(previous[0][0], "").trimEnd(), labels, baseVersion });
    if (JSON.stringify(parsed) !== JSON.stringify(expected)) throw new Error("Frozen candidate cannot be changed");
    return { body: existing, manifest: parsed };
  }
  const manifest = makeManifest({ baseline, commit, pr, version: draft.tag_name, notes: existing, labels, baseVersion });
  return { body: `${existing.trimEnd()}\n\n${MARKER}${JSON.stringify(manifest)} -->`, manifest };
}
/** Retain a pending candidate or select the oldest unreleased merge. */
export function chooseCandidate({ merges, pending = null, published = new Set(), baseline }) {
  validBaseline(baseline);
  if (pending) return { create: false, pending: true, ...pending, baseline };
  const candidate = merges.find((m) => m.commit !== baseline && !published.has(m.commit));
  return candidate ? { create: true, pending: true, ...candidate, baseline } : { create: false, pending: false, baseline };
}

/** Execute a command with argument boundaries and return its trimmed output. */
function run(command, args) { return execFileSync(command, args, { encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] }).trim(); }
/** Call the API without exposing response bodies or credentials in errors. */
export function makeGhClient({ exec = execFileSync, repository = REPOSITORY } = {}) {
  return async (path, method = "GET", body) => {
    const args = ["api", "--include", "-H", "Accept: application/vnd.github+json", "-X", method, `/repos/${repository}/${path}`];
    if (body !== undefined) args.push("--input", "-");
    let raw;
    let failure;
    try {
      raw = exec("gh", args, { encoding: "utf8", input: body === undefined ? undefined : JSON.stringify(body), env: process.env, stdio: ["pipe", "pipe", "pipe"], maxBuffer: 64 * 1024 * 1024 });
    } catch (error) {
      raw = error.stdout ?? "";
      failure = error;
    }
    const response = String(raw).replaceAll("\r\n", "\n");
    const status = Number(/^HTTP\/\S+ (\d{3})/m.exec(response)?.[1]);
    const subprocessFailure = failure && (failure.code === "ENOBUFS" || failure.code === "ETIMEDOUT" || !status || status < 400);
    if (subprocessFailure || !status || status >= 400) {
      const error = new Error(`GitHub API request failed (${method} ${path}; status ${subprocessFailure ? "unknown" : status || "unknown"})`);
      error.status = subprocessFailure ? null : status || null;
      throw error;
    }
    const payload = response.slice(response.indexOf("\n\n") + 2).trim();
    return payload ? JSON.parse(payload) : null;
  };
}
/** Append scalar candidate fields to the GitHub Actions output file. */
function output(values) {
  const lines = Object.entries(values).filter(([, v]) => v !== undefined).map(([k, v]) => `${k}=${String(v)}`);
  if (process.env.GITHUB_OUTPUT) writeFileSync(process.env.GITHUB_OUTPUT, lines.join("\n") + "\n", { flag: "a" });
}
/** Require a merged pull request for the exact main commit. */
function assertPR(pr, commit) {
  if (!pr?.merged_at || pr.base?.ref !== "main" || pr.base?.repo?.full_name !== REPOSITORY || pr.merge_commit_sha?.toLowerCase() !== commit?.toLowerCase()) throw new Error("Candidate pull request does not match merged main commit");
  return releaseLabels(pr.labels);
}
/** Require a successful CI push run for this repository and exact merge. */
async function successfulCI(api, commit) {
  const runs = await api(`actions/workflows/ci.yml/runs?head_sha=${commit}&event=push&branch=main`);
  const matching = runs?.workflow_runs?.filter((r) => r.head_sha === commit && r.event === "push" && r.head_branch === "main" && r.repository?.full_name === REPOSITORY && r.head_repository?.full_name === REPOSITORY) ?? [];
  matching.sort((a, b) => Number(b.id) - Number(a.id));
  if (matching[0]?.status !== "completed" || matching[0]?.conclusion !== "success") throw new Error("Candidate CI is missing or unsuccessful");
}

/** Reject baselines outside the main first-parent history. */
function historyAfter(git, baseline) {
  const history = git("git", ["rev-list", "--first-parent", "origin/main"]).split("\n").filter(Boolean);
  const index = history.indexOf(validBaseline(baseline));
  if (index < 0) throw new Error("Release baseline is not in main first-parent history");
  return history.slice(0, index).reverse();
}

/** Read every API page without treating malformed responses as an empty list. */
async function listPages(api, path) {
  const result = [];
  for (let page = 1; ; page++) {
    const items = await api(`${path}${path.includes("?") ? "&" : "?"}per_page=100&page=${page}`);
    if (!Array.isArray(items)) throw new Error("Malformed paginated API response");
    result.push(...items);
    if (items.length < 100) return result;
  }
}
/** Require an annotated version tag that names the exact candidate commit. */
async function tagCommit(api, version, expected) {
  const ref = await api(`git/ref/tags/${version}`);
  if (ref?.object?.type !== "tag") throw new Error("Release tag is not annotated");
  const tag = await api(`git/tags/${ref.object.sha}`);
  if (tag?.tag !== version || tag?.object?.type !== "commit" || tag.object.sha !== expected) throw new Error("Release tag points to a different commit or tag identity");
}
/** Check frozen identity, main history, pull request, CI, and the release tag. */
export async function verifyCandidate({ api, git = run, draft, baseline, id, version, commit, allowPublished = false, requireTag = true }) {
  const m = parseManifest(draft.body);
  if (String(draft.id) !== String(id) || draft.tag_name !== m.version || draft.target_commitish !== m.commit || draft.prerelease !== false || m.baseline !== validBaseline(baseline) || (!allowPublished && draft.draft !== true)) throw new Error("Draft identity or baseline does not match manifest");
  if (version && version !== m.version || commit && commit !== m.commit) throw new Error("Candidate inputs do not match frozen candidate");
  const history = historyAfter(git, m.baseline);
  if (!history.includes(m.commit)) throw new Error("Candidate is not in main first-parent history after baseline");
  const pr = await api(`pulls/${m.pr}`);
  assertPR(pr, m.commit);
  await successfulCI(api, m.commit);
  if (requireTag) await tagCommit(api, m.version, m.commit);
  return m;
}
/** Create an immutable annotated tag and dispatch its verified release workflow. */
export async function dispatchCandidate({ api, git = run, draft, id, baseline, version, commit }) {
  const manifest = await verifyCandidate({ api, git, draft, id, baseline, version, commit, requireTag: false });
  if (!Array.isArray(draft.assets) || draft.assets.length !== 0) throw new Error("Candidate has a partial asset upload; owner recovery is required");
  let existing;
  try { await tagCommit(api, manifest.version, manifest.commit); existing = true; }
  catch (e) { if (e.status !== 404) throw e; }
  if (!existing) {
    const tag = await api("git/tags", "POST", { tag: manifest.version, message: `Release ${manifest.version}`, object: manifest.commit, type: "commit" });
    try { await api("git/refs", "POST", { ref: `refs/tags/${manifest.version}`, sha: tag.sha }); }
    catch (e) { if (e.status !== 422) throw e; }
    await tagCommit(api, manifest.version, manifest.commit);
  }
  await api("actions/workflows/release.yml/dispatches", "POST", { ref: manifest.version, inputs: { draft_id: String(id), candidate_commit: manifest.commit } });
  return manifest;
}
/** Recover the oldest eligible candidate from main history and release records. */
export async function selectCandidate({ api, git = run, baseline }) {
  baseline = validBaseline(baseline);
  git("git", ["fetch", "--no-tags", "origin", "refs/heads/main:refs/remotes/origin/main"]);
  const commits = historyAfter(git, baseline);
  const merges = [];
  for (const commit of commits) {
    const prs = await listPages(api, `commits/${commit}/pulls`);
    const matching = prs.filter((pr) => pr.merged_at && pr.base?.ref === "main" && pr.base?.repo?.full_name === REPOSITORY && pr.merge_commit_sha === commit);
    if (matching.length > 1) throw new Error("Multiple pull requests match the same main merge");
    if (matching.length) merges.push({ commit, pr: matching[0].number });
  }
  const releases = await listPages(api, "releases");
  const drafts = releases.filter((r) => r.draft);
  const managed = drafts.filter((r) => String(r.body ?? "").includes(MARKER));
  if (managed.length > 1 || drafts.length !== managed.length) throw new Error("Unrelated or multiple draft releases exist; refusing to proceed");
  const published = new Set();
  for (const release of releases.filter((r) => !r.draft && String(r.body ?? "").includes(MARKER))) {
    const manifest = parseManifest(release.body);
    if (manifest.baseline !== baseline || release.tag_name !== manifest.version || release.target_commitish !== manifest.commit || !commits.includes(manifest.commit) || release.prerelease || release.assets?.length !== 14) throw new Error("Published release candidate identity is invalid");
    await tagCommit(api, manifest.version, manifest.commit);
    if (published.has(manifest.commit)) throw new Error("Duplicate published release candidate");
    published.add(manifest.commit);
  }
  const result = chooseCandidate({ merges, published, baseline });
  if (managed.length) {
    const draft = managed[0];
    if (!Array.isArray(draft.assets) || draft.assets.length !== 0) throw new Error("Candidate has a partial asset upload; owner recovery is required");
    const manifest = await verifyCandidate({ api, git, draft, baseline, id: draft.id, requireTag: false });
    if (!result.pending || manifest.commit !== result.commit || manifest.pr !== result.pr) throw new Error("Pending candidate is not the oldest unreleased merge");
    return { create: false, pending: true, baseline, draft_id: draft.id, version: manifest.version, commit: manifest.commit, pr: manifest.pr };
  }
  if (result.create) {
    const pr = await api(`pulls/${result.pr}`);
    assertPR(pr, result.commit);
    await successfulCI(api, result.commit);
    result.base_version = previousVersion(releases);
    result.from = validBaseline(git("git", ["rev-parse", `${result.commit}^1`]));
  }
  return result;
}
/** Validate mode inputs before reading or changing a release candidate. */
export async function runMode({ mode, api, git = run, env = process.env }) {
  const baseline = validBaseline(env.RELEASE_BASELINE);
  if (mode !== "select" && !/^[1-9]\d*$/.test(String(env.DRAFT_ID ?? ""))) throw new Error("DRAFT_ID must be a positive integer");
  if (mode === "select") return selectCandidate({ api, git, baseline });
  if (mode === "freeze") {
    if (!/^[1-9]\d*$/.test(String(env.CANDIDATE_PR ?? ""))) throw new Error("CANDIDATE_PR must be a positive integer");
    if (!historyAfter(git, baseline).includes(env.CANDIDATE_COMMIT)) throw new Error("Candidate is not in main first-parent history after baseline");
    await successfulCI(api, env.CANDIDATE_COMMIT);
    const draft = await api(`releases/${env.DRAFT_ID}`), pr = await api(`pulls/${env.CANDIDATE_PR}`);
    if (String(draft.id) !== String(env.DRAFT_ID)) throw new Error("Draft ID does not match the requested candidate");
    assertPR(pr, env.CANDIDATE_COMMIT);
    const frozen = freezeDraft(draft, { baseline, commit: env.CANDIDATE_COMMIT, pr: env.CANDIDATE_PR, labels: pr.labels, baseVersion: env.BASE_VERSION });
    if (frozen.body !== draft.body) await api(`releases/${draft.id}`, "PATCH", { body: frozen.body });
    return { draft_id: draft.id, version: frozen.manifest.version, commit: frozen.manifest.commit, pr: frozen.manifest.pr };
  }
  if (!["verify", "dispatch"].includes(mode)) throw new Error("Mode must be select, freeze, verify, or dispatch");
  if (!env.DRAFT_ID) throw new Error("DRAFT_ID is required");
  const draft = await api(`releases/${env.DRAFT_ID}`);
  const args = { api, git, draft, id: env.DRAFT_ID, baseline, version: env.VERSION, commit: env.CANDIDATE_COMMIT };
  const m = mode === "dispatch" ? await dispatchCandidate(args) : await verifyCandidate(args);
  return { draft_id: String(env.DRAFT_ID), version: m.version, commit: m.commit, pr: m.pr };
}
/** Restrict CLI execution to the expected repository and report safe errors. */
async function main() {
  if (process.env.GITHUB_REPOSITORY !== REPOSITORY) throw new Error(`GITHUB_REPOSITORY must be ${REPOSITORY}`);
  const result = await runMode({ mode: process.argv[2], api: makeGhClient() });
  output(result);
}
if (process.argv[1] && import.meta.url === new URL(`file://${process.argv[1]}`).href) main().catch((e) => { console.error(e.message); process.exitCode = 1; });
