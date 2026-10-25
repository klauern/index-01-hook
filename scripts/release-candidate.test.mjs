import test from "node:test";
import { execFileSync } from "node:child_process";
import assert from "node:assert/strict";
import {
  REPOSITORY, chooseCandidate, dispatchCandidate, expectedVersion, freezeDraft,
  makeGhClient, parseManifest, releaseLabels, runMode, selectCandidate,
  validBaseline, verifyCandidate,
} from "./release-candidate.mjs";

const baseline = "a".repeat(40);
const commit1 = "b".repeat(40);
const commit2 = "c".repeat(40);
const tagSHA = "d".repeat(40);
const identity = { full_name: REPOSITORY };
const frozenArgs = { baseline, commit: commit1, pr: 2, labels: [], baseVersion: "v1.2.2" };

/** Build a synthetic stable draft without frozen metadata. */
function rawDraft(overrides = {}) {
  return { id: 7, draft: true, prerelease: false, target_commitish: commit1, tag_name: "v1.2.3", body: "Release notes", assets: [], ...overrides };
}
/** Build a synthetic frozen candidate for verification tests. */
function frozenDraft(overrides = {}) {
  const draft = rawDraft();
  return { ...draft, body: freezeDraft(draft, frozenArgs).body, ...overrides };
}
/** Create a synthetic HTTP error with a typed status. */
function apiError(status) {
  return Object.assign(new Error(`Synthetic HTTP ${status}`), { status });
}
/** Provide isolated Git and API fixtures that record attempted writes. */
function fixture({ releases = [], badCI = null, tagExists = false, race = false, dispatchFailure = false } = {}) {
  const writes = [];
  const reads = [];
  const refs = new Map(tagExists ? [["v1.2.3", commit1]] : []);
  const prs = new Map([
    [2, { number: 2, merged_at: "2026-01-01T00:00:00Z", base: { ref: "main", repo: identity }, merge_commit_sha: commit1, labels: [] }],
    [3, { number: 3, merged_at: "2026-01-02T00:00:00Z", base: { ref: "main", repo: identity }, merge_commit_sha: commit2, labels: [] }],
  ]);
  const git = (command, args) => {
    assert.equal(command, "git");
    if (args[0] === "fetch") return "";
    if (args[0] === "rev-list") return [commit2, commit1, baseline].join("\n");
    if (args[0] === "rev-parse") return args[1] === `${commit1}^1` ? baseline : commit1;
    throw new Error(`Unexpected Git arguments ${args}`);
  };
  const api = async (path, method = "GET", body) => {
    if (method !== "GET") writes.push({ path, method, body });
    else reads.push(path);
    if (path.startsWith("commits/")) {
      const sha = path.split("/")[1];
      return [...prs.values()].filter((pr) => pr.merge_commit_sha === sha);
    }
    if (path.startsWith("pulls/")) return prs.get(Number(path.split("/")[1]));
    if (path.startsWith("releases?")) return releases;
    if (path.startsWith("releases/")) {
      const release = releases.find((r) => String(r.id) === path.split("/")[1]);
      if (!release) throw apiError(404);
      if (method === "PATCH") Object.assign(release, body);
      return release;
    }
    if (path.startsWith("actions/workflows/ci.yml/runs?")) {
      const sha = new URLSearchParams(path.split("?")[1]).get("head_sha");
      const run = { id: 1, head_sha: sha, head_branch: "main", event: "push", status: "completed", conclusion: "success", repository: identity, head_repository: identity };
      return { workflow_runs: badCI ? badCI(run) : [run] };
    }
    if (path.startsWith("git/ref/tags/")) {
      const version = path.slice("git/ref/tags/".length);
      if (!refs.has(version)) throw apiError(404);
      return { ref: `refs/tags/${version}`, object: { type: "tag", sha: tagSHA } };
    }
    if (path === `git/tags/${tagSHA}`) return { tag: "v1.2.3", object: { type: "commit", sha: refs.get("v1.2.3") } };
    if (path === "git/tags" && method === "POST") return { sha: tagSHA };
    if (path === "git/refs" && method === "POST") {
      refs.set(body.ref.slice("refs/tags/".length), commit1);
      if (race) throw apiError(422);
      return { object: { type: "tag", sha: tagSHA } };
    }
    if (path === "actions/workflows/release.yml/dispatches" && method === "POST") {
      if (dispatchFailure) throw apiError(503);
      return null;
    }
    throw new Error(`Unexpected API request ${method} ${path}`);
  };
  return { api, git, writes, reads, refs, prs, releases };
}

// These tests do not contact GitHub or execute Git commands.
test("strict baselines and label conflicts fail closed", () => {
  assert.equal(validBaseline(baseline.toUpperCase()), baseline);
  for (const value of [undefined, "", "abc", "g".repeat(40)]) assert.throws(() => validBaseline(value));
  assert.deepEqual(releaseLabels([]), []);
  for (const label of ["patch", "minor", "major"]) assert.deepEqual(releaseLabels([`release:${label}`]), [`release:${label}`]);
  for (const labels of [["release:major", "release:minor"], ["release:patch", "release:minor"], [null], null]) assert.throws(() => releaseLabels(labels));
});

test("version validation uses patch by default and explicit major or minor", () => {
  assert.equal(expectedVersion("v1.2.2", []), "v1.2.3");
  assert.equal(expectedVersion("v1.2.2", ["feature", "bug"]), "v1.2.3");
  assert.equal(expectedVersion("v1.2.2", ["release:patch"]), "v1.2.3");
  assert.equal(expectedVersion("v1.2.2", ["release:minor"]), "v1.3.0");
  assert.equal(expectedVersion("v0.3.0", ["release:major"]), "v1.0.0");
  assert.equal(expectedVersion("v99999999999999999999.0.0", ["release:major"]), "v100000000000000000000.0.0");
  assert.throws(() => expectedVersion("v01.2.3", []));
});

test("freezing is idempotent and rejects modified notes, labels, assets, and versions", () => {
  const draft = rawDraft();
  const frozen = freezeDraft(draft, frozenArgs);
  assert.equal(parseManifest(frozen.body).commit, commit1);
  assert.equal(freezeDraft({ ...draft, body: frozen.body }, frozenArgs).body, frozen.body);
  assert.throws(() => parseManifest(frozen.body.replace("Release notes", "Edited notes")), /changed/);
  assert.throws(() => freezeDraft({ ...draft, body: frozen.body }, { ...frozenArgs, labels: ["release:minor"] }), /version/);
  assert.throws(() => freezeDraft({ ...draft, target_commitish: commit2 }, frozenArgs), /target commit/);
  assert.throws(() => freezeDraft({ ...draft, assets: [{}] }, frozenArgs), /assets/);
  assert.throws(() => freezeDraft({ ...draft, tag_name: "latest" }, frozenArgs), /version/);
  assert.throws(() => freezeDraft({ ...draft, tag_name: "v2.0.0" }, frozenArgs), /labels/);
  assert.throws(() => parseManifest("notes"), /exactly one/);
  assert.throws(() => parseManifest(`${frozen.body}\n${frozen.body}`), /exactly one/);
  assert.throws(() => parseManifest(frozen.body.replace('"pr":2', '"pr":0')), /manifest/);
  assert.throws(() => freezeDraft({ ...draft, prerelease: true }, frozenArgs), /stable draft/);
});

test("queue selection keeps the oldest merge and supports an empty queue", () => {
  const merges = [{ commit: commit1, pr: 2 }, { commit: commit2, pr: 3 }];
  assert.equal(chooseCandidate({ merges, baseline }).commit, commit1);
  assert.equal(chooseCandidate({ merges, baseline, published: new Set([commit1]) }).commit, commit2);
  assert.equal(chooseCandidate({ merges: [], baseline }).pending, false);
});

test("two rapid merges select only the first exact tested commit and its first parent", async () => {
  const f = fixture({ releases: [{ id: 1, draft: false, tag_name: "v1.2.2" }] });
  const result = await selectCandidate({ ...f, baseline });
  assert.deepEqual(result, { create: true, pending: true, baseline, commit: commit1, pr: 2, from: baseline, base_version: "v1.2.2" });
  assert.equal(f.writes.length, 0);
});

test("a frozen draft is retained while later merges wait", async () => {
  const f = fixture({ releases: [frozenDraft()] });
  const result = await selectCandidate({ ...f, baseline });
  assert.equal(result.create, false);
  assert.equal(result.commit, commit1);
  assert.equal(result.version, "v1.2.3");
  assert.equal(result.draft_id, 7);
  assert.equal(f.writes.length, 0);
});

test("a verified published candidate advances the queue without requiring retained CI logs", async () => {
  const published = frozenDraft({ draft: false, assets: Array.from({ length: 14 }, (_, id) => ({ id })) });
  const f = fixture({ releases: [published], tagExists: true });
  const result = await selectCandidate({ ...f, baseline });
  assert.equal(result.commit, commit2);
  assert.equal(result.from, commit1);
  assert.equal(result.base_version, "v1.2.3");
  assert.equal(f.reads.some((p) => p.startsWith("actions/") && p.includes(commit1)), false);
});

test("queue rejects unrelated drafts, invalid published markers, and invalid baselines", async () => {
  const unrelated = fixture({ releases: [rawDraft()] });
  await assert.rejects(selectCandidate({ ...unrelated, baseline }), /Unrelated/);
  const edited = fixture({ releases: [frozenDraft({ draft: false, body: frozenDraft().body.replace("Release notes", "Edited") })] });
  await assert.rejects(selectCandidate({ ...edited, baseline }), /changed/);
  const outside = fixture();
  await assert.rejects(selectCandidate({ ...outside, baseline: "f".repeat(40) }), /first-parent/);
  const mismatched = fixture({ releases: [frozenDraft({ target_commitish: commit2 })] });
  await assert.rejects(selectCandidate({ ...mismatched, baseline }), /identity/);
});

test("missing, failed, canceled, wrong-branch, or untrusted CI blocks the oldest merge", async () => {
  const variants = [
    () => [],
    (r) => [{ ...r, conclusion: "failure" }],
    (r) => [{ ...r, conclusion: "cancelled" }],
    (r) => [{ ...r, status: "in_progress", conclusion: null }],
    (r) => [{ ...r, head_sha: commit2 }],
    (r) => [{ ...r, head_branch: "feature" }],
    (r) => [{ ...r, head_repository: { full_name: "other/repository" } }],
    (r) => [r, { ...r, id: 2, conclusion: "failure" }],
  ];
  for (const badCI of variants) {
    const f = fixture({ badCI });
    await assert.rejects(selectCandidate({ ...f, baseline }), /CI/);
    assert.equal(f.writes.length, 0);
  }
});

test("verification checks ID, version, commit, main PR, frozen notes, and annotated tag", async () => {
  const draft = frozenDraft();
  const f = fixture({ releases: [draft], tagExists: true });
  assert.equal((await verifyCandidate({ ...f, draft, baseline, id: 7 })).commit, commit1);
  for (const args of [{ id: 9 }, { version: "v9.0.0" }, { commit: commit2 }, { draft: { ...draft, draft: false } }]) {
    await assert.rejects(verifyCandidate({ ...f, draft, baseline, id: 7, ...args }));
  }
  f.prs.get(2).base.ref = "feature";
  await assert.rejects(verifyCandidate({ ...f, draft, baseline, id: 7 }), /main commit/);
  f.prs.get(2).base.ref = "main";
  f.refs.set("v1.2.3", commit2);
  await assert.rejects(verifyCandidate({ ...f, draft, baseline, id: 7 }), /different commit/);
  const noTag = fixture();
  await assert.rejects(verifyCandidate({ ...noTag, draft, baseline, id: 7 }), /404/);
});

test("freeze mode records only the validated exact draft and repeats without a write", async () => {
  const f = fixture({ releases: [rawDraft()] });
  const env = { RELEASE_BASELINE: baseline, DRAFT_ID: "7", CANDIDATE_PR: "2", CANDIDATE_COMMIT: commit1, BASE_VERSION: "v1.2.2" };
  await runMode({ ...f, mode: "freeze", env });
  assert.equal(f.writes.length, 1);
  assert.equal(f.writes[0].method, "PATCH");
  assert.equal(parseManifest(f.releases[0].body).base_version, "v1.2.2");
  await runMode({ ...f, mode: "freeze", env });
  assert.equal(f.writes.length, 1);
  await assert.rejects(runMode({ ...f, mode: "verify", env: { ...env, DRAFT_ID: "../other" } }), /positive integer/);
});

test("dispatch creates one annotated tag and explicitly dispatches the tag-ref workflow", async () => {
  const draft = frozenDraft();
  const f = fixture({ releases: [draft] });
  await dispatchCandidate({ ...f, draft, baseline, id: 7 });
  assert.deepEqual(f.writes.map((w) => w.path), ["git/tags", "git/refs", "actions/workflows/release.yml/dispatches"]);
  assert.deepEqual(f.writes[0].body, { tag: "v1.2.3", message: "Release v1.2.3", object: commit1, type: "commit" });
  assert.deepEqual(f.writes[2].body, { ref: "v1.2.3", inputs: { draft_id: "7", candidate_commit: commit1 } });
  await dispatchCandidate({ ...f, draft, baseline, id: 7 });
  assert.equal(f.writes.filter((w) => w.path === "git/refs").length, 1);
});

test("dispatch reconciles only an exact tag race and rejects conflicting or lightweight tags", async () => {
  const draft = frozenDraft();
  const race = fixture({ releases: [draft], race: true });
  await dispatchCandidate({ ...race, draft, baseline, id: 7 });
  const wrong = fixture({ releases: [draft], tagExists: true });
  wrong.refs.set("v1.2.3", commit2);
  await assert.rejects(dispatchCandidate({ ...wrong, draft, baseline, id: 7 }), /different commit/);
  assert.equal(wrong.writes.length, 0);
  const lightweight = fixture();
  const api = (path, ...args) => path.startsWith("git/ref/") ? { object: { type: "commit", sha: commit1 } } : lightweight.api(path, ...args);
  await assert.rejects(dispatchCandidate({ ...lightweight, api, draft, baseline, id: 7 }), /not annotated/);
  assert.equal(lightweight.writes.length, 0);
});

test("a dispatch failure retains the tag for recovery and a published draft cannot dispatch", async () => {
  const draft = frozenDraft();
  const f = fixture({ releases: [draft], dispatchFailure: true });
  await assert.rejects(dispatchCandidate({ ...f, draft, baseline, id: 7 }), /503/);
  assert.equal(f.refs.get("v1.2.3"), commit1);
  const published = fixture();
  await assert.rejects(dispatchCandidate({ ...published, draft: { ...draft, draft: false }, baseline, id: 7 }), /identity/);
  assert.equal(published.writes.length, 0);
});

test("the real gh adapter handles HTTP 204 and typed failures without leaking response bodies", async () => {
  const calls = [];
  const empty = makeGhClient({ exec: (command, args, options) => { calls.push({ command, args, options }); return "HTTP/2.0 204 No Content\r\nServer: synthetic\r\n\r\n"; } });
  assert.equal(await empty("actions/workflows/release.yml/dispatches", "POST", { ref: "v1.2.3" }), null);
  assert.equal(calls[0].args.includes("--include"), true);
  assert.equal(calls[0].options.input, '{"ref":"v1.2.3"}');
  assert.equal(calls[0].options.maxBuffer, 64 * 1024 * 1024);
  const json = makeGhClient({ exec: (command, args, options) => execFileSync(process.execPath, ["-e", "process.stdout.write('HTTP/2.0 200 OK\\nX-Pad: ' + 'x'.repeat(1024 * 1024 + 10) + '\\n\\n{\\\"id\\\":7}\\n')"], options) });
  assert.deepEqual(await json("releases"), { id: 7 });
  assert.deepEqual(await json("releases/7"), { id: 7 });
  for (const status of [403, 404, 422, 503]) {
    const client = makeGhClient({ exec: () => { throw Object.assign(new Error("private diagnostic"), { status: 1, stdout: `HTTP/2.0 ${status} Synthetic\n\n{\"message\":\"private response\"}` }); } });
    await assert.rejects(client("git/ref/tags/v1.2.3"), (error) => error.status === status && !error.message.includes("private"));
  }
  const overflow = makeGhClient({ exec: () => { throw Object.assign(new Error("private child output"), { code: "ENOBUFS", stdout: "HTTP/2.0 200 OK\\nprivate response" }); } });
  await assert.rejects(overflow("releases"), (error) => error.status === null && /status unknown/.test(error.message) && !error.message.includes("private"));
});

test("partial uploads stop queue selection and redispatch before writes", async () => {
  const draft = frozenDraft({ assets: [{ id: 1 }] });
  const f = fixture({ releases: [draft] });
  await assert.rejects(selectCandidate({ ...f, baseline }), /partial asset upload/);
  await assert.rejects(dispatchCandidate({ ...f, draft, baseline, id: 7 }), /partial asset upload/);
  assert.equal(f.writes.length, 0);
});
