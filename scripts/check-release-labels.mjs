const RELEASE_LABELS = new Set([
  "release:major",
  "release:minor",
  "release:patch",
]);

export function checkReleaseLabels(labels) {
  if (!Array.isArray(labels) || labels.some((label) =>
    typeof label !== "string" || label.length === 0
  )) {
    return { valid: false, error: "Malformed labels" };
  }

  const selected = labels.filter((label) => RELEASE_LABELS.has(label));
  if (selected.length > 1) {
    return { valid: false, error: "Conflicting release labels" };
  }
  return { valid: true };
}

if (process.argv[1] && import.meta.url === new URL(`file://${process.argv[1]}`).href) {
  let input = "";
  process.stdin.setEncoding("utf8");
  process.stdin.on("data", (chunk) => { input += chunk; });
  process.stdin.on("end", () => {
    try {
      const payload = JSON.parse(input);
      const labels = payload?.pull_request?.labels;
      const result = checkReleaseLabels(labels?.map((label) => label?.name));
      if (!result.valid) {
        console.error(result.error);
        process.exitCode = 1;
      }
    } catch {
      console.error("Malformed event payload");
      process.exitCode = 1;
    }
  });
}
