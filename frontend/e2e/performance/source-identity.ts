import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import path from "node:path";

/** Scope is explicit: exact HEAD plus current bytes at the measured boundaries. */
export function sourceIdentity() {
	const root = execFileSync("git", ["rev-parse", "--show-toplevel"], { encoding: "utf8" }).trim();
	const paths = [
		"frontend/src/renderer/hooks/useConversation.ts",
		"frontend/src/renderer/components/chat/ChatWorkspace.tsx",
		"frontend/src/renderer/components/chat/ChatTimelineItems.tsx",
		"frontend/src/renderer/components/chat/ActivityRun.tsx",
		"frontend/src/renderer/components/chat/TurnWorkSummary.tsx",
		"frontend/e2e/chat-performance.spec.ts",
		"frontend/e2e/performance/harness.tsx",
		"frontend/e2e/performance/chat-open-measurement.ts",
		"frontend/e2e/performance/large-turn-fixture.ts",
		"frontend/e2e/performance/fixture-identity.ts",
		"frontend/e2e/performance/source-identity.ts",
	].sort();
	const files = paths.map((file) => ({ path: file,
		sha256: createHash("sha256").update(readFileSync(path.join(root, file))).digest("hex") }));
	return {
		head: execFileSync("git", ["rev-parse", "HEAD"], { cwd: root, encoding: "utf8" }).trim(),
		dirty: Boolean(execFileSync("git", ["status", "--porcelain"], { cwd: root, encoding: "utf8" }).trim()),
		sourceSha256: createHash("sha256").update(JSON.stringify(files)).digest("hex"),
		sourceHashScope: "listed-files-current-bytes; not whole bundle",
		files,
	};
}
