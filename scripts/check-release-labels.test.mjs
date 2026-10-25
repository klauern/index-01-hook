import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync, mkdtempSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { spawnSync } from "node:child_process";
import { checkReleaseLabels } from "./check-release-labels.mjs";

test("accepts no release label and one release label", () => {
  assert.deepEqual(checkReleaseLabels(["feature"]), { valid: true });
  for (const label of ["release:major", "release:minor", "release:patch"]) {
    assert.deepEqual(checkReleaseLabels([label]), { valid: true });
  }
});

test("rejects every pair of conflicting release labels", () => {
  const labels = ["release:major", "release:minor", "release:patch"];
  for (let first = 0; first < labels.length; first += 1) {
    for (let second = first + 1; second < labels.length; second += 1) {
      assert.equal(checkReleaseLabels([labels[first], labels[second]]).valid, false);
    }
  }
});

test("fails closed for malformed inputs", () => {
  for (const input of [undefined, null, "release:patch", [null], [""]]) {
    assert.equal(checkReleaseLabels(input).valid, false);
  }
});

test("workflow inline checker matches tested checker outcomes", () => {
  const workflow = readFileSync(new URL("../.github/workflows/release-labels.yml", import.meta.url), "utf8");
  const runBlock = workflow.split("        run: |\n")[1]?.split("      - name:")[0];
  assert.ok(runBlock, "workflow run block exists");
  const command = runBlock.split("\n").filter(Boolean).map((line) => line.slice(10)).join("\n");
  const cases = [
    { labels: [], expected: true },
    { labels: [{ name: "feature" }], expected: true },
    { labels: [{ name: "release:patch" }], expected: true },
    { labels: [{ name: "release:major" }, { name: "release:minor" }], expected: false },
    { labels: null, expected: false },
    { labels: [{ name: null }], expected: false },
  ];
  const directory = mkdtempSync(join(tmpdir(), "release-labels-"));
  try {
    const eventPath = join(directory, "event.json");
    for (const { labels, expected } of cases) {
      writeFileSync(eventPath, JSON.stringify({ pull_request: { labels } }));
      const result = spawnSync("bash", ["-c", command], {
        encoding: "utf8",
        env: { ...process.env, GITHUB_EVENT_PATH: eventPath },
      });
      assert.equal(result.status === 0, expected, result.stderr);
      assert.equal(checkReleaseLabels(Array.isArray(labels) ? labels.map((label) => label?.name) : labels).valid, expected);
    }
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});
