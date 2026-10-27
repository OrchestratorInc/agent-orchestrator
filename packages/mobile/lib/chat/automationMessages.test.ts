/**
 * The same templates the desktop's readers are tested against: file feedback
 * from the shared formatter, the daemon's nudges as fixed samples of
 * lifecycle's formatters.
 */
import { describe, expect, it } from "vitest";
import { formatFileAnnotationMessage, formatFileAnnotationMessages } from "../../../shared/files/file-annotations";
import {
	artifactEntry,
	automationOf,
	forgeURL,
	humanVisibleText,
	inlineRuns,
	latestWorkerUpdateKeys,
	piggybackedUpdates,
	readAutomation,
	sessionLink,
	splitArtifacts,
	splitHeadline,
	workerUpdatesOf,
} from "./automationMessages";
import type { ConversationMessage } from "./types";

const PR = 'PR #6974 "feat: link this computer" (ao/remote/this-computer → main)';
const URL = "https://github.com/o/r/pull/6974";

const ciFailing = [
	`CI is failing on ${PR}.`,
	"",
	"Failed: renderer-smoke (failed)",
	"Failure URL: https://github.com/o/r/actions/runs/1/job/2",
	"",
	"Log tail (last 2 lines):",
	"```",
	"npm ERR! smoke failed",
	"exit 1",
	"```",
	"",
	"Failed: lint (failed)",
	"",
	"Use the included log tail and failure URL first; fetch full CI logs only if you need additional context. Fix the issues and push again.",
	`PR: ${URL}`,
].join("\n");

const reviewComment = [
	`The following 1 unresolved review comment(s) are on ${PR} as of just now. You should not need to re-fetch this data unless you need additional context.`,
	"",
	"1. frontend/src/ThisComputerAccess.tsx:97 (@illegalcall):",
	"A failed turn-on does not undo what it started.",
	"",
	"The switch then shows **off**.",
	"   https://github.com/o/r/pull/6974#discussion_r1",
	"   Thread ID: PRRT_1",
	"",
	"Address each comment and push fixes. Use the thread ID to resolve each thread directly after pushing when available.",
	`PR: ${URL}`,
].join("\n");

const changesRequested = [
	`A changes-requested review from @whoisasx is on ${PR}.`,
	"",
	"Review body:",
	"The migration IDs collide with shipped migrations.",
	"",
	"Review URL: https://github.com/o/r/pull/6974#pullrequestreview-1",
	"Review ID: PRR_1",
	"",
	"Address the requested changes and push.",
].join("\n");

const reports = [
	"Reports since your previous turn:",
	"",
	"[done] [Fix table rendering on mobile app](ao://sessions/agent-orchestrator-mo/agent-orchestrator-mo-259)",
	"PR #7291 open with iOS/Android before-after screenshots; all CI checks pass.",
	"pr_created: https://github.com/o/r/pull/7291",
	"",
	"[needs_input] ao://sessions/agent-orchestrator-mo/agent-orchestrator-mo-241",
	"Approved PR #7113 on GitHub. Not merged.",
	"artifact: /tmp/review.md",
].join("\n");

function message(text: string, extra: Partial<ConversationMessage> = {}): ConversationMessage {
	return { kind: "message", id: "m1", sequence: 1, revision: 0, role: "user", origin: "automation", text, streaming: false, createdAt: "2026-10-20T12:32:00Z", ...extra };
}

describe("reading AO's automation messages", () => {
	it("reads file feedback from the shared formatter's message", () => {
		const one = formatFileAnnotationMessage(
			{ source: "Workspace", path: "backend/guard.go", side: "new", line: 131, newLine: 131, lineText: "const slowLeaseHold = 5 * time.Second", scope: "committed", workspaceVersion: "f138c8", fileFingerprint: "1182ac" },
			"why is this required?",
		);
		expect(readAutomation(one)).toEqual({
			kind: "fileFeedback",
			comments: [{ path: "backend/guard.go", line: 131, code: "const slowLeaseHold = 5 * time.Second", quote: undefined, feedback: "why is this required?" }],
		});
		const [batch] = formatFileAnnotationMessages([
			{ target: { path: "a.ts", side: "new", line: 3, newLine: 3 }, feedback: "rename this" },
			{ target: { path: "b.ts", side: "file" }, feedback: "split this file" },
		]);
		expect(readAutomation(batch!.message)).toMatchObject({
			kind: "fileFeedback",
			comments: [{ path: "a.ts", line: 3, feedback: "rename this" }, { path: "b.ts", line: undefined, wholeFile: true }],
		});
	});

	it("reads the git buttons' instructions", () => {
		expect(readAutomation("Commit any uncommitted changes with a clear message, push this branch, and open a pull request against `main`. Give it a descriptive title and summary."))
			.toEqual({ kind: "gitAction", action: "createPR", base: "main" });
		expect(readAutomation("Commit all uncommitted changes with a clear message and push this branch to update PR #12.")).toEqual({ kind: "gitAction", action: "commitPush" });
		expect(readAutomation("Push this branch.")).toEqual({ kind: "gitAction", action: "push" });
	});

	it("reads what GitHub reported on the PR", () => {
		const pr = { number: 6974, title: "feat: link this computer", url: URL };
		expect(readAutomation(ciFailing)).toEqual({
			kind: "ciFailing",
			pr,
			checks: [
				{ name: "renderer-smoke", url: "https://github.com/o/r/actions/runs/1/job/2", log: "npm ERR! smoke failed\nexit 1" },
				{ name: "lint", url: undefined, log: undefined },
			],
		});
		expect(readAutomation(reviewComment)).toEqual({
			kind: "reviewComments",
			pr,
			comments: [{ path: "frontend/src/ThisComputerAccess.tsx", line: 97, author: "illegalcall", body: "A failed turn-on does not undo what it started.\n\nThe switch then shows **off**." }],
		});
		expect(readAutomation(changesRequested)).toEqual({
			kind: "changesRequested",
			pr: { ...pr, url: "https://github.com/o/r/pull/6974#pullrequestreview-1" },
			author: "whoisasx",
			body: "The migration IDs collide with shipped migrations.",
		});
		expect(readAutomation(`There are merge conflicts on ${PR}. Rebase onto the base branch and resolve them.\nPR: ${URL}`)).toEqual({ kind: "mergeConflict", pr });
	});

	it("keeps the card for comments that do not number as announced", () => {
		expect(readAutomation(reviewComment.replace("The following 1 ", "The following 2 "))).toBeNull();
	});

	it("reads AO's notices", () => {
		expect(readAutomation("Read and follow the AO review task in `/tmp/task.md`.")).toEqual({ kind: "notice", notice: "reviewTask" });
	});

	it("never reads an agent's message as AO's", () => {
		expect(automationOf(message(ciFailing, { senderSessionId: "w-1" }))).toBeNull();
		expect(automationOf(message(`[from w-1] ${ciFailing}`))).toBeNull();
		expect(automationOf(message(ciFailing, { origin: "human" }))).toBeNull();
		expect(automationOf(message(ciFailing))).toMatchObject({ kind: "ciFailing" });
	});

	it("links only to the forges", () => {
		expect(forgeURL(URL)).toBe(URL);
		expect(forgeURL("https://evil.example/pull/1")).toBeUndefined();
		expect(forgeURL("http://github.com/o/r")).toBeUndefined();
	});
});

describe("worker reports", () => {
	it("reads each report in a batch, named or not", () => {
		const updates = workerUpdatesOf(message(reports));
		expect(updates).toMatchObject([
			{ state: "done", name: "Fix table rendering on mobile app", projectId: "agent-orchestrator-mo", sessionId: "agent-orchestrator-mo-259" },
			{ state: "needs_input", name: "agent-orchestrator-mo-241", sessionId: "agent-orchestrator-mo-241" },
		]);
		expect(splitArtifacts(updates[1]!.body)).toEqual({ text: "Approved PR #7113 on GitHub. Not merged.", artifacts: ["/tmp/review.md"] });
	});

	it("reads a direct message from its sender", () => {
		expect(workerUpdatesOf(message('[from w-7 "Shrink tile"] On it.'))).toMatchObject([{ name: "Shrink tile", sessionId: "w-7", body: "On it." }]);
		expect(workerUpdatesOf(message("On it.", { senderSessionId: "w-7", senderDisplayName: "Tile" }))).toMatchObject([{ name: "Tile", body: "On it." }]);
	});

	it("takes reports folded into a human message out of what the user typed", () => {
		const human = message(`can u check what review agents could be stopped\n\n<ao-worker-reports>\n${reports}\n</ao-worker-reports>`, { origin: "human", id: "h1" });
		expect(humanVisibleText(human.text)).toBe("can u check what review agents could be stopped");
		expect(piggybackedUpdates(human)).toHaveLength(2);
		expect(workerUpdatesOf(human)).toEqual([]);
		expect(humanVisibleText("no reports")).toBe("no reports");
	});

	it("knows each worker's newest report", () => {
		const first = message(reports, { id: "a" });
		const second = message("Reports since your previous turn:\n\n[done] ao://sessions/agent-orchestrator-mo/agent-orchestrator-mo-241\nAnswered.", { id: "b" });
		expect([...latestWorkerUpdateKeys([first, second])].sort()).toEqual(["a:0", "b:0"]);
	});

	it("headlines a report by its short first line, else its first sentence", () => {
		expect(splitHeadline("Done\nthe rest")).toEqual({ headline: "Done", detail: "the rest" });
		expect(splitHeadline("Opened [#7237](https://x). More here.")).toEqual({ headline: "Opened #7237", detail: "Opened [#7237](https://x). More here." });
	});
});

describe("opening a report's links and artifacts", () => {
	it("finds an artifact's entry in its session's artifact folder", () => {
		expect(artifactEntry("/Users/x/.ao/data/artifacts/ao-230/review/audit.md", "ao-230")).toBe("review/audit.md");
		expect(artifactEntry("audit.md", "ao-230")).toBe("audit.md");
		expect(artifactEntry("./shots/a.png", "ao-230")).toBe("shots/a.png");
		expect(artifactEntry("/Users/x/.ao/data/artifacts/ao-999/audit.md", "ao-230")).toBeUndefined();
		expect(artifactEntry("/tmp/audit.md", "ao-230")).toBeUndefined();
		expect(artifactEntry("/a/artifacts/ao-230/../secrets", "ao-230")).toBeUndefined();
		expect(artifactEntry("audit.md", undefined)).toBeUndefined();
	});

	it("keeps a report's links, code and emphasis as runs", () => {
		expect(inlineRuns("Opened [#7237](https://github.com/o/r/pull/7237); see https://github.com/o/r/pull/1. Ran `npm test` **twice**."))
			.toEqual([
				{ text: "Opened " },
				{ text: "#7237", href: "https://github.com/o/r/pull/7237" },
				{ text: "; see " },
				{ text: "https://github.com/o/r/pull/1", href: "https://github.com/o/r/pull/1" },
				{ text: ". Ran " },
				{ text: "npm test", code: true },
				{ text: " " },
				{ text: "twice", strong: true },
				{ text: "." },
			]);
		expect(inlineRuns("no markup")).toEqual([{ text: "no markup" }]);
	});

	it("reads the session an ao://sessions link names", () => {
		expect(sessionLink("ao://sessions/agent-orchestrator-mo/agent-orchestrator-mo-259")).toEqual({ projectId: "agent-orchestrator-mo", sessionId: "agent-orchestrator-mo-259" });
		expect(sessionLink("ao://sessions/p")).toBeUndefined();
		expect(sessionLink("https://github.com")).toBeUndefined();
	});
});
