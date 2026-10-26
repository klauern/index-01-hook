import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const config = readFileSync(new URL("../.github/release-drafter.yml", import.meta.url), "utf8");
const workflow = readFileSync(new URL("../.github/workflows/release-drafter.yml", import.meta.url), "utf8");

test("version resolver uses explicit release labels and patch fallback", () => {
  const resolverSection = config.split("  - title: 'Features'")[0];
  const resolverCategories = resolverSection.split(/(?=^  - type: version-resolver$)/m).filter((part) => part.startsWith("  - type: version-resolver"));
  assert.equal(resolverCategories.length, 4);
  assert.match(resolverCategories[0], /semver-increment: major\s+when:\s+label: release:major/);
  assert.match(resolverCategories[1], /semver-increment: minor\s+when:\s+label: release:minor/);
  assert.match(resolverCategories[2], /semver-increment: patch\s+when:\s+label: release:patch/);
  assert.match(resolverCategories[3], /semver-increment: patch/);
  assert.doesNotMatch(resolverCategories[3], /when:/);
});

test("note categories do not set release increments", () => {
  const noteCategories = config.split(/(?=^  - title:)/m).filter((part) => part.startsWith("  - title:"));
  assert.equal(noteCategories.length, 4);
  for (const category of noteCategories) assert.doesNotMatch(category, /semver-increment:/);
  assert.match(config, /when:\s+labels:\s+- feature\s+- enhancement/);
  assert.match(config, /when:\s+labels:\s+- bug\s+- fix/);
  assert.match(config, /when:\s+labels:\s+- chore\s+- maintenance/);
  const otherChanges = noteCategories.find((category) => category.includes("title: 'Other changes'"));
  assert.ok(otherChanges, "Other changes category must exist as the fallback");
  assert.doesNotMatch(otherChanges, /when:/);
});

test("workflow pins v7 and bounds comparison and target to selected commits", () => {
  assert.match(workflow, /uses: release-drafter\/release-drafter@72967cdc98ddd3160f1fc3dff619de365e137dd0 # v7\.9\.0/);
  assert.match(workflow, /token: \$\{\{ github\.token \}\}/);
  assert.match(workflow, /from: \$\{\{ steps\.select\.outputs\.from \}\}/);
  assert.match(workflow, /commitish: \$\{\{ steps\.select\.outputs\.commit \}\}/);
  assert.match(workflow, /publish: false/);
  assert.match(workflow, /RESOLVED_VERSION: \$\{\{ steps\.drafter\.outputs\.tag_name \}\}/);
  assert.match(workflow, /name: Create the automatic release tag and dispatch/);
  assert.match(workflow, /environment: public-release/);
  assert.match(workflow, /vars\.RELEASE_DRAFTER_PUBLISH == 'true'/);
  assert.match(workflow, /vars\.PUBLIC_RELEASE_APPROVED == 'true'/);
  assert.doesNotMatch(workflow, /disable-autolabeler/);
});
