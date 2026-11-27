/**
 * AO's automation messages are read back from the text AO writes for them. The
 * templates this renderer writes are produced here by their real formatters, so
 * a change to one fails the reader it breaks; the daemon's are fixed samples of
 * lifecycle's formatters.
 */

import { cleanup, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactElement } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { formatDiffSelectionMessage } from "../../../shared/diff-selection";
import { formatFileAnnotationMessage, formatFileAnnotationMessages } from "../../../shared/file-annotations";
import type { ConversationMessage } from "../../types/conversation";
import { gitActionMessage, otherPRConflictMessage } from "../SessionBranchSummary";
import { TooltipProvider } from "../ui/tooltip";
import { openLinkInSystemBrowser } from "../../lib/external-link-policy";
import { useUiStore } from "../../stores/ui-store";
import { AutomationMessage, pullRequestBursts, readAutomation, reviewBursts } from "./AutomationMessage";

vi.mock("../../lib/external-link-policy", async (importOriginal) => ({
	...(await importOriginal<typeof import("../../lib/external-link-policy")>()),
	openLinkInSystemBrowser: vi.fn(async () => {}),
}));

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
	"Address each comment and push fixes. Use the thread ID to resolve each thread directly after pushing when available. You should not need to re-fetch review data unless you need additional context beyond what is provided here.",
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
	"Address the requested changes and push. You should not need to re-fetch the review unless you need additional context beyond what Orchestrator has provided here.",
].join("\n");

const mergeConflict = `There are merge conflicts on ${PR}. Rebase onto the base branch and resolve them.\nPR: ${URL}`;

const otherPRConflict = (pr: object) => otherPRConflictMessage(pr as Parameters<typeof otherPRConflictMessage>[0]);

function message(text: string, extra: Partial<ConversationMessage> = {}): ConversationMessage {
	return { kind: "message", id: "m1", sequence: 1, revision: 0, role: "user", origin: "automation", text, streaming: false, createdAt: "2026-10-20T12:32:00Z", ...extra };
}

function draw(text: string, extra: Partial<ConversationMessage> = {}, queued = false) {
	return render(<TooltipProvider><AutomationMessage message={message(text, extra)} queued={queued} fallback={<p>card</p>} /></TooltipProvider> as ReactElement);
}

describe("reading AO's automation messages", () => {
	it("reads file feedback from the message the file view sends", () => {
		const one = formatFileAnnotationMessage(
			{ source: "Workspace", path: "backend/internal/sessionguard/guard.go", side: "new", line: 131, newLine: 131, lineText: "const slowLeaseHold = 5 * time.Second", scope: "committed", workspaceVersion: "f138c8", fileFingerprint: "1182ac" },
			"why is this required?",
		);
		expect(readAutomation(one)).toEqual({
			kind: "fileFeedback",
			comments: [{ path: "backend/internal/sessionguard/guard.go", line: 131, code: "const slowLeaseHold = 5 * time.Second", quote: undefined, feedback: "why is this required?" }],
		});

		const [batch] = formatFileAnnotationMessages([
			{ target: { path: "a.ts", side: "new", line: 3, newLine: 3 }, feedback: "rename this" },
			{ target: { path: "b.ts", side: "file" }, feedback: "split this file" },
		]);
		expect(readAutomation(batch!.message)).toMatchObject({
			kind: "fileFeedback",
			comments: [{ path: "a.ts", line: 3, feedback: "rename this" }, { path: "b.ts", line: undefined, wholeFile: true, feedback: "split this file" }],
		});
	});

	it("names no line for feedback on a deleted line, which the file no longer has", () => {
		const deleted = formatFileAnnotationMessage({ path: "guard.go", side: "old", line: 120, oldLine: 120, lineText: "return nil" }, "why remove this?");
		expect(readAutomation(deleted)).toMatchObject({ kind: "fileFeedback", comments: [{ path: "guard.go", line: undefined, wholeFile: undefined, code: "return nil" }] });
	});

	it("reads a diff selection and the git buttons' instructions", () => {
		const selection = formatDiffSelectionMessage({
			instruction: "Use a constant here.",
			filePath: "src/app.ts",
			lines: [{ kind: "add", oldNo: null, newNo: 10, text: "a" }, { kind: "add", oldNo: null, newNo: 12, text: "b" }],
		});
		expect(readAutomation(selection)).toEqual({ kind: "diffSelection", path: "src/app.ts", start: 10, end: 12, instruction: "Use a constant here." });
		const fromDeleted = formatDiffSelectionMessage({
			instruction: "Keep this.",
			filePath: "src/app.ts",
			lines: [{ kind: "del", oldNo: 40, newNo: null, text: "a" }, { kind: "add", oldNo: null, newNo: 42, text: "b" }],
		});
		expect(readAutomation(fromDeleted)).toMatchObject({ kind: "diffSelection", start: undefined, end: undefined });
		const pathLike = formatDiffSelectionMessage({ instruction: "File: rename to util.ts", filePath: "src/app.ts", lines: [{ kind: "add", oldNo: null, newNo: 10, text: "a" }] });
		expect(readAutomation(pathLike)).toMatchObject({ path: "src/app.ts", instruction: "File: rename to util.ts" });

		expect(readAutomation(gitActionMessage("createPR", "main", undefined))).toEqual({ kind: "gitAction", action: "createPR", base: "main" });
		expect(readAutomation(gitActionMessage("createDraftPR", undefined, undefined))).toEqual({ kind: "gitAction", action: "createDraftPR", base: undefined });
		expect(readAutomation(gitActionMessage("commit", "main", undefined))).toEqual({ kind: "gitAction", action: "commit" });
		expect(readAutomation(gitActionMessage("commitPush", "main", 12))).toEqual({ kind: "gitAction", action: "commitPush" });
		expect(readAutomation(gitActionMessage("push", "main", undefined))).toEqual({ kind: "gitAction", action: "push" });
		expect(readAutomation(gitActionMessage("updateBase", "main", 7534))).toEqual({ kind: "gitAction", action: "updateBase", base: "main" });
		expect(readAutomation(gitActionMessage("updateBase", undefined, undefined))).toEqual({ kind: "gitAction", action: "updateBase" });
		expect(readAutomation(gitActionMessage("resolveConflicts", "main", 7534, ["src/a.tsx", "src/a.test.tsx"]))).toEqual({
			kind: "gitAction", action: "resolveConflicts", base: "main", files: ["src/a.tsx", "src/a.test.tsx"],
		});
		expect(readAutomation(gitActionMessage("resolveConflicts", undefined, undefined))).toEqual({ kind: "gitAction", action: "resolveConflicts" });
		expect(readAutomation(otherPRConflict({ number: 8, sourceBranch: "ao/feature", targetBranch: "main", mergeability: { state: "conflicting", conflictFiles: [{ path: "src/a.tsx" }] } }))).toEqual({
			kind: "gitAction", action: "resolveConflicts", base: "main", prNumber: 8, files: ["src/a.tsx"],
		});
		expect(readAutomation(otherPRConflict({ number: 8, mergeability: { state: "conflicting" } }))).toEqual({
			kind: "gitAction", action: "resolveConflicts", prNumber: 8,
		});
	});

	it("leaves a conflict message whose base or PR number does not repeat to the card", () => {
		expect(readAutomation(gitActionMessage("resolveConflicts", "main", undefined).replace("up to date with `main`", "up to date with `release`"))).toBeNull();
		expect(readAutomation(otherPRConflict({ number: 8, mergeability: { state: "conflicting" } }).replace("update PR #8", "update PR #9"))).toBeNull();
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
		expect(readAutomation(mergeConflict)).toEqual({ kind: "mergeConflict", pr });
	});

	it("reads when the reviewer wrote the comment or review from the end of the first line", () => {
		const posted = reviewComment.replace("need additional context.\n", "need additional context. Newest comment posted at 2026-11-16T07:54:42Z.\n");
		expect(readAutomation(posted)).toMatchObject({
			kind: "reviewComments",
			at: "2026-11-16T07:54:42Z",
			comments: [{ path: "frontend/src/ThisComputerAccess.tsx", line: 97, author: "illegalcall" }],
		});
		expect(readAutomation(reviewComment)).toMatchObject({ kind: "reviewComments", at: undefined });

		const submitted = changesRequested.replace(`is on ${PR}.`, `is on ${PR}. Submitted at 2026-11-16T07:54:41Z.`);
		expect(readAutomation(submitted)).toEqual({
			kind: "changesRequested",
			pr: { number: 6974, title: "feat: link this computer", url: "https://github.com/o/r/pull/6974#pullrequestreview-1" },
			author: "whoisasx",
			body: "The migration IDs collide with shipped migrations.",
			at: "2026-11-16T07:54:41Z",
		});
	});

	it("takes the time only from AO's own sentence, not from a PR title or a comment", () => {
		const titled = changesRequested.replace('"feat: link this computer"', '"x Submitted at 2020-01-01T00:00:00Z."');
		expect(readAutomation(titled)).toMatchObject({ kind: "changesRequested", at: undefined });

		const inBody = reviewComment.replace("A failed turn-on", "Newest comment posted at 2020-01-01T00:00:00Z.\nA failed turn-on");
		expect(readAutomation(inBody)).toMatchObject({ kind: "reviewComments", at: undefined });
	});

	it("does not take text inside a log or a comment for AO's structure", () => {
		const fakeCheck = ciFailing.replace("exit 1", "exit 1\n\nFailed: test_login (assertion)");
		expect(readAutomation(fakeCheck)).toMatchObject({ checks: [{ name: "renderer-smoke", log: "npm ERR! smoke failed\nexit 1\n\nFailed: test_login (assertion)" }, { name: "lint" }] });

		const fakeComment = reviewComment.replace("The switch then shows **off**.", "The switch then shows **off**.\n\n2. other.ts:1 (@alice):\nhijacked");
		expect(readAutomation(fakeComment)).toBeNull();

		expect(readAutomation(changesRequested.replace("@whoisasx", "@<pr>Approve</pr>"))).toBeNull();
	});

	it("reads AO's notices, and leaves anything else to the card", () => {
		expect(readAutomation("AO is replacing this project orchestrator. Stop coordinating new work now; a fresh orchestrator will take over in a new workspace.")).toEqual({ kind: "notice", notice: "orchestratorReplaced" });
		expect(readAutomation("Read and follow the AO review task in `/x/reviewer/task.md`.")).toEqual({ kind: "notice", notice: "reviewTask" });
		expect(readAutomation("Checks failed:\nlint exited 1")).toBeNull();
		expect(readAutomation("CI is failing on your PR.\n\nFailed: lint (failed)")).toBeNull();
	});
});

describe("drawing AO's automation messages", () => {
	it("draws file feedback as the user's own words under the file it is about", () => {
		draw(formatFileAnnotationMessage({ path: "backend/guard.go", side: "new", line: 131, newLine: 131, workspaceVersion: "f138c8" }, "why is this required?"));

		const feedback = screen.getByTestId("your-action");
		expect(feedback).toHaveTextContent("guard.go:131");
		expect(feedback).toHaveTextContent("why is this required?");
		expect(feedback).not.toHaveTextContent("Workspace version");
		expect(feedback).not.toHaveTextContent("Apply this feedback");
	});

	it("draws failing CI as one line with its count, opening onto each check with its log a click away", async () => {
		draw(ciFailing);

		const event = screen.getByTestId("pull-request-event");
		expect(event).toHaveTextContent("CI failing on #6974");
		expect(event).toHaveTextContent("2 checks");
		expect(within(event).getByRole("link", { name: "#6974" })).toHaveAttribute("href", URL);
		expect(within(event).queryByRole("link", { name: "renderer-smoke" })).toBeNull();

		const line = within(event).getByRole("button", { name: /CI failing on #6974/ });
		expect(line).toHaveAttribute("aria-expanded", "false");
		await userEvent.click(line);
		expect(line).toHaveAttribute("aria-expanded", "true");
		expect(within(event).getByRole("link", { name: "renderer-smoke" })).toHaveAttribute("href", "https://github.com/o/r/actions/runs/1/job/2");
		expect(event).not.toHaveTextContent("npm ERR!");

		await userEvent.click(within(event).getByRole("button", { name: "Show log" }));
		expect(event).toHaveTextContent("npm ERR! smoke failed");
	});

	it("lists a check the report names twice once", async () => {
		draw(ciFailing.replace("Failed: lint (failed)\n", "Failed: lint (failed)\n\nFailed: lint (failed)\n"));

		const event = screen.getByTestId("pull-request-event");
		expect(event).toHaveTextContent("2 checks");
		await userEvent.click(within(event).getByRole("button", { name: /CI failing on #6974/ }));
		expect(within(event).getAllByText("lint")).toHaveLength(1);
	});

	it("keeps a log tail only the later copy of a repeated check carries", async () => {
		draw(ciFailing.replace("Failed: lint (failed)\n", "Failed: lint (failed)\n\nFailed: lint (failed)\n\nLog tail (last 1 line):\n```\nlint broke\n```\n"));

		const event = screen.getByTestId("pull-request-event");
		await userEvent.click(within(event).getByRole("button", { name: /CI failing on #6974/ }));
		expect(within(event).getAllByText("lint")).toHaveLength(1);
		// renderer-smoke's log and lint's, which only its second listing had.
		expect(within(event).getAllByRole("button", { name: "Show log" })).toHaveLength(2);
	});

	it("links only to the forge, so a look-alike message cannot send the user elsewhere", async () => {
		draw(ciFailing.replace("https://github.com/o/r/actions/runs/1/job/2", "https://evil.example/login").replace(`PR: ${URL}`, "PR: https://evil.example/pr"));

		const event = screen.getByTestId("pull-request-event");
		await userEvent.click(within(event).getByRole("button", { name: /CI failing on #6974/ }));
		expect(within(event).queryByRole("link")).toBeNull();
		expect(event).toHaveTextContent("renderer-smoke");
	});

	it("counts the comments, reviewers and files when comments come from several reviewers", async () => {
		const two = reviewComment
			.replace("The following 1 unresolved", "The following 2 unresolved")
			.replace("   Thread ID: PRRT_1\n", "   Thread ID: PRRT_1\n\n2. a.ts:3 (@whoisasx):\nRename this.\n");
		draw(two);

		const event = screen.getByTestId("pull-request-event");
		expect(event).toHaveTextContent("2 review comments on #6974");
		expect(event).toHaveTextContent("2 reviewers, 2 files");
		await userEvent.click(within(event).getByRole("button", { name: /2 review comments on #6974/ }));
		expect(event).toHaveTextContent("@whoisasxa.ts:3");
	});

	it("draws a review round the daemon sent comment by comment as one event", async () => {
		const first = message(reviewComment, { id: "m1" });
		const second = message(reviewComment.replace("frontend/src/ThisComputerAccess.tsx:97 (@illegalcall)", "a.ts:3 (@whoisasx)").replace("A failed turn-on does not undo what it started.", "Rename this."), { id: "m2", sequence: 2 });
		const other = message(reviewComment.replace(PR, PR.replace("#6974", "#7000")), { id: "m3", sequence: 3 });
		const bursts = reviewBursts([first, second, other]);
		expect(bursts.get("m1")).toEqual([first, second]);
		expect(bursts.get("m2")).toBeNull();
		// A lone comment, here on another PR, draws as itself.
		expect(bursts.has("m3")).toBe(false);

		render(<TooltipProvider><AutomationMessage message={first} burst={bursts.get("m1")} fallback={<p>card</p>} /><AutomationMessage message={second} burst={bursts.get("m2")} fallback={<p>card</p>} /></TooltipProvider> as ReactElement);
		const events = screen.getAllByTestId("pull-request-event");
		expect(events).toHaveLength(1);
		expect(events[0]).toHaveTextContent("2 review comments on #6974");
		await userEvent.click(within(events[0]!).getByRole("button", { name: /2 review comments on #6974/ }));
		expect(events[0]).toHaveTextContent("A failed turn-on does not undo what it started.");
		expect(events[0]).toHaveTextContent("Rename this.");
	});

	it("names the one reviewer of a review round", () => {
		const first = message(reviewComment, { id: "m1" });
		const second = message(reviewComment.replace("frontend/src/ThisComputerAccess.tsx:97", "a.ts:3"), { id: "m2", sequence: 2 });
		const bursts = reviewBursts([first, second]);
		render(<TooltipProvider><AutomationMessage message={first} burst={bursts.get("m1")} fallback={<p>card</p>} /></TooltipProvider> as ReactElement);
		expect(screen.getByTestId("pull-request-event")).toHaveTextContent("@illegalcall, 2 files");
	});

	it("keeps a human message that reads like a review nudge out of the burst", () => {
		const pasted = message(reviewComment, { id: "h1", origin: "human" });
		const first = message(reviewComment, { id: "m1" });
		const second = message(reviewComment, { id: "m2" });
		const bursts = reviewBursts([pasted, first, second]);
		expect(bursts.has("h1")).toBe(false);
		expect(bursts.get("m1")).toEqual([first, second]);
		expect(bursts.get("m2")).toBeNull();
	});

	it("times a PR event by when it happened on the forge, not when AO delivered it", () => {
		draw(reviewComment.replace("need additional context.\n", "need additional context. Newest comment posted at 2026-11-16T07:54:42Z.\n"), { createdAt: "2026-11-16T08:26:00Z" });
		expect(screen.getByTestId("pull-request-event").querySelector("time")).toHaveAttribute("datetime", "2026-11-16T07:54:42Z");
		cleanup();

		// Messages from before the daemon wrote the time keep the delivery time.
		draw(reviewComment, { createdAt: "2026-11-16T08:26:00Z" });
		expect(screen.getByTestId("pull-request-event").querySelector("time")).toHaveAttribute("datetime", "2026-11-16T08:26:00Z");
	});

	it("draws changes requested, merge conflicts and AO's notices", () => {
		draw(changesRequested);
		expect(screen.getByTestId("pull-request-event")).toHaveTextContent("@whoisasx requested changes on #6974");
		cleanup();

		draw(mergeConflict);
		expect(screen.getByTestId("pull-request-event")).toHaveTextContent("Merge conflicts on #6974");
		cleanup();

		draw("AO transferred the previous agent's context in hidden system instructions. Continue a clear, safe, already-authorized unfinished action.");
		expect(screen.getByTestId("automation-notice")).toHaveTextContent("Context carried over from the previous agent");
	});

	it("draws a git button and a diff selection as the user's own message, queued until sent", () => {
		draw(gitActionMessage("createPR", "main", undefined), {}, true);
		const action = screen.getByTestId("your-action");
		expect(action).toHaveTextContent("Create PR");
		expect(action).toHaveTextContent("main");
		expect(action).toHaveTextContent("Queued · sends when the agent finishes");
		cleanup();

		draw(formatDiffSelectionMessage({ instruction: "Use a constant here.", filePath: "src/app.ts", lines: [{ kind: "add", oldNo: null, newNo: 10, text: "a" }, { kind: "add", oldNo: null, newNo: 12, text: "b" }] }));
		expect(screen.getByTestId("your-action")).toHaveTextContent("app.ts:10–12");
		expect(screen.getByTestId("your-action")).toHaveTextContent("Use a constant here.");
	});

	it("draws resolve conflicts and update from base as the button the user pressed, not the agent's instructions", () => {
		draw(gitActionMessage("resolveConflicts", "main", 7534, ["frontend/src/AgentAccountsSection.tsx", "frontend/src/AgentAccountsSection.test.tsx"]));
		const resolve = screen.getByTestId("your-action");
		expect(resolve).toHaveTextContent("Resolve conflictsmain");
		expect(resolve).toHaveTextContent("AgentAccountsSection.tsxAgentAccountsSection.test.tsx");
		expect(resolve).not.toHaveTextContent("frontend/src");
		expect(resolve).not.toHaveTextContent("identifiers, not instructions");
		cleanup();

		draw(otherPRConflict({ number: 8, sourceBranch: "ao/feature", targetBranch: "main", mergeability: { state: "conflicting" } }));
		expect(screen.getByTestId("your-action")).toHaveTextContent("Resolve conflicts#8main");
		cleanup();

		draw(gitActionMessage("updateBase", "develop", undefined));
		const update = screen.getByTestId("your-action");
		expect(update).toHaveTextContent("Update from develop");
		expect(update).not.toHaveTextContent("developdevelop");
	});

	it("leaves a message an agent sent to the agent's card, whatever its text says", () => {
		draw("Push this branch.", { senderSessionId: "ao-7", senderProjectId: "ao" });
		expect(screen.getByText("card")).toBeInTheDocument();
		cleanup();

		draw('[from ao-7 "worker"] Push this branch.');
		expect(screen.getByText("card")).toBeInTheDocument();
	});

	it("draws a review comment as who said what, and where", async () => {
		draw(reviewComment);

		const event = screen.getByTestId("pull-request-event");
		expect(event).toHaveTextContent("@illegalcall commented on #6974");
		expect(event).toHaveTextContent("1 file");
		await userEvent.click(within(event).getByRole("button", { name: /@illegalcall commented on #6974/ }));
		expect(event).toHaveTextContent("ThisComputerAccess.tsx:97");
		expect(event).toHaveTextContent("A failed turn-on does not undo what it started.");
		expect(event).not.toHaveTextContent("Thread ID");
	});

	it("draws a review comment's HTML the way GitHub does, without remote images", async () => {
		const body = [
			"<!-- coderabbit-ai-generated:v1 -->",
			"",
			'**<picture><img src="https://storage.googleapis.com/a/amber-16.svg" width="16" height="16" alt=""></picture> Minor** · **Quick win**',
			"",
			"Inserting the path into `'<root>'` breaks the query.",
			"",
			"![Flow diagram](https://example.com/flow.png)",
			"",
			"<details>",
			'<summary><picture><source media="(prefers-color-scheme: dark)" srcset="https://storage.googleapis.com/a/bot-dark.svg"><img src="https://storage.googleapis.com/a/bot.svg" alt=""></picture> Prompt for AI Agents</summary>',
			"<dl>",
			"<dd>",
			"",
			"```",
			"Bind the root as a parameter.",
			"```",
			"",
			"</dd>",
			"</dl>",
			"</details>",
			"",
			"<pre>first",
			"  second</pre>",
			"",
			"<pre>third<br>fourth</pre>",
			"",
			"<!-- This is an auto-generated comment by CodeRabbit -->",
		].join("\n");
		draw(reviewComment.replace("A failed turn-on does not undo what it started.\n\nThe switch then shows **off**.", body));

		const event = screen.getByTestId("pull-request-event");
		await userEvent.click(within(event).getByRole("button", { name: /commented on #6974/ }));
		expect(event).toHaveTextContent("Minor · Quick win");
		expect(event).toHaveTextContent("Inserting the path into '<root>' breaks the query.");
		expect(event).not.toHaveTextContent("<!--");
		expect(event).not.toHaveTextContent("<picture");
		expect(event).not.toHaveTextContent("storage.googleapis.com");
		expect(event).toHaveTextContent("Flow diagram");
		expect(event).not.toHaveTextContent("example.com/flow.png");
		expect(event.querySelector("img, picture, source")).toBeNull();
		const details = event.querySelector("details");
		expect(details).not.toHaveAttribute("open");
		expect(details?.querySelector("summary")).toHaveTextContent("Prompt for AI Agents");
		expect(details).toHaveTextContent("Bind the root as a parameter.");
		expect([...event.querySelectorAll("pre")].map((pre) => pre.textContent)).toContain("first\n  second");
		expect([...event.querySelectorAll("pre")].map((pre) => pre.textContent)).toContain("third\nfourth");

		await userEvent.click(within(details as HTMLElement).getByText("Prompt for AI Agents"));
		expect(details).toHaveAttribute("open");
		await userEvent.click(await within(event).findByRole("button", { name: "Show less" }));
		expect(details).not.toHaveAttribute("open");
	});

	it("keeps the card for anything it cannot read", () => {
		draw("Checks failed:\nlint exited 1");

		expect(screen.getByText("card")).toBeInTheDocument();
	});
});

// Developer Mode draws a PR's review comments and failing CI as one right-aligned line.
describe("drawing PR feedback in Developer Mode", () => {
	beforeEach(() => useUiStore.setState({ developerMode: true }));
	afterEach(() => {
		cleanup();
		useUiStore.setState({ developerMode: false });
	});

	it("draws failing CI as one line with its count, opening onto each check with its log a click away", async () => {
		draw(ciFailing);

		const line = screen.getByTestId("pull-request-feedback");
		expect(line).toHaveTextContent("2 failing checks on #6974");
		expect(within(line).getByRole("link", { name: "#6974" })).toHaveAttribute("href", URL);
		expect(within(line).queryByRole("link", { name: "renderer-smoke" })).toBeNull();

		const toggle = within(line).getByRole("button", { name: /2 failing checks on #6974/ });
		expect(toggle).toHaveAttribute("aria-expanded", "false");
		await userEvent.click(toggle);
		expect(toggle).toHaveAttribute("aria-expanded", "true");
		expect(within(line).getByRole("link", { name: "renderer-smoke" })).toHaveAttribute("href", "https://github.com/o/r/actions/runs/1/job/2");
		expect(line).not.toHaveTextContent("npm ERR!");

		await userEvent.click(within(line).getByRole("button", { name: "Show log" }));
		expect(line).toHaveTextContent("npm ERR! smoke failed");
	});

	it("lists a check the report names twice once", async () => {
		draw(ciFailing.replace("Failed: lint (failed)\n", "Failed: lint (failed)\n\nFailed: lint (failed)\n"));

		const line = screen.getByTestId("pull-request-feedback");
		expect(line).toHaveTextContent("2 failing checks");
		await userEvent.click(within(line).getByRole("button", { name: /2 failing checks on #6974/ }));
		expect(within(line).getAllByText("lint")).toHaveLength(1);
	});

	it("keeps a log tail only the later copy of a repeated check carries", async () => {
		draw(ciFailing.replace("Failed: lint (failed)\n", "Failed: lint (failed)\n\nFailed: lint (failed)\n\nLog tail (last 1 line):\n```\nlint broke\n```\n"));

		const line = screen.getByTestId("pull-request-feedback");
		await userEvent.click(within(line).getByRole("button", { name: /failing checks on #6974/ }));
		expect(within(line).getAllByText("lint")).toHaveLength(1);
		// renderer-smoke's log and lint's, which only its second listing had.
		expect(within(line).getAllByRole("button", { name: "Show log" })).toHaveLength(2);
	});

	it("links only to the forge, so a look-alike message cannot send the user elsewhere", async () => {
		draw(ciFailing.replace("https://github.com/o/r/actions/runs/1/job/2", "https://evil.example/login").replace(`PR: ${URL}`, "PR: https://evil.example/pr"));

		const line = screen.getByTestId("pull-request-feedback");
		await userEvent.click(within(line).getByRole("button", { name: /failing checks on #6974/ }));
		expect(within(line).queryByRole("link")).toBeNull();
		expect(line).toHaveTextContent("renderer-smoke");
	});

	it("opens a comment's changes from anywhere on its row, outside a chat that can open the file", async () => {
		vi.mocked(openLinkInSystemBrowser).mockClear();
		draw(reviewComment);

		const line = screen.getByTestId("pull-request-feedback");
		await userEvent.click(within(line).getByRole("button", { name: /1 comment from illegalcall on #6974/ }));
		await userEvent.click(within(line).getByText("A failed turn-on does not undo what it started."));
		expect(openLinkInSystemBrowser).toHaveBeenLastCalledWith(`${URL}/files`);

		// The file label is the same control, for the keyboard.
		await userEvent.click(within(line).getByRole("button", { name: "ThisComputerAccess.tsx:97" }));
		expect(openLinkInSystemBrowser).toHaveBeenCalledTimes(2);
	});

	it("names the reviewers and says whose each comment is when there are several", async () => {
		const two = reviewComment
			.replace("The following 1 unresolved", "The following 2 unresolved")
			.replace("   Thread ID: PRRT_1\n", "   Thread ID: PRRT_1\n\n2. a.ts:3 (@whoisasx):\nRename this.\n");
		draw(two);

		const line = screen.getByTestId("pull-request-feedback");
		expect(line).toHaveTextContent("2 comments from illegalcall and whoisasx on #6974");
		await userEvent.click(within(line).getByRole("button", { name: /2 comments from illegalcall and whoisasx on #6974/ }));
		expect(line).toHaveTextContent("whoisasx·a.ts:3");
	});

	it("counts more than two reviewers, shows three avatars and the rest as a number, people before bots", () => {
		const authors = ["coderabbitai[bot]", "illegalcall", "whoisasx", "octocat"];
		const four = reviewComment
			.replace("The following 1 unresolved", "The following 4 unresolved")
			.replace("1. frontend/src/ThisComputerAccess.tsx:97 (@illegalcall):", "1. frontend/src/ThisComputerAccess.tsx:97 (@coderabbitai[bot]):")
			.replace("   Thread ID: PRRT_1\n", `   Thread ID: PRRT_1\n${authors.slice(1).map((author, index) => `\n${index + 2}. a.ts:${index + 3} (@${author}):\nRename this.\n`).join("")}`);
		draw(four);

		const line = screen.getByTestId("pull-request-feedback");
		// "+1" sits beside the avatars and is left out of what a screen reader hears.
		expect(within(line).getByRole("button", { name: /^4 comments from 4 reviewers on #6974/ })).toBeInTheDocument();
		expect(line).toHaveTextContent("+1");
		// The bot's square avatar is the one left out: people come first.
		const avatars = [...line.querySelectorAll("img")].map((img) => img.getAttribute("src"));
		expect(avatars).toEqual(["illegalcall", "whoisasx", "octocat"].map((login) => `https://github.com/${login}.png?size=40`));
	});

	it("draws a PR's comments and failing CI that came back to back as one line", async () => {
		const first = message(reviewComment, { id: "m1" });
		const second = message(reviewComment.replace("frontend/src/ThisComputerAccess.tsx:97 (@illegalcall)", "a.ts:3 (@whoisasx)").replace("A failed turn-on does not undo what it started.", "Rename this."), { id: "m2", sequence: 2 });
		const checks = message(ciFailing, { id: "m3", sequence: 3 });
		const other = message(reviewComment.replace(PR, PR.replace("#6974", "#7000")), { id: "m4", sequence: 4 });
		const bursts = pullRequestBursts([first, second, checks, other]);
		expect(bursts.get("m1")).toEqual([first, second, checks]);
		expect(bursts.get("m2")).toBeNull();
		expect(bursts.get("m3")).toBeNull();
		// Feedback on another PR is its own line.
		expect(bursts.get("m4")).toEqual([other]);

		render(<TooltipProvider>{[first, second, checks].map((each) => <AutomationMessage key={each.id} message={each} burst={bursts.get(each.id)} fallback={<p>card</p>} />)}</TooltipProvider> as ReactElement);
		const lines = screen.getAllByTestId("pull-request-feedback");
		expect(lines).toHaveLength(1);
		expect(lines[0]).toHaveTextContent("2 comments from illegalcall and whoisasx and 2 failing checks on #6974");
		await userEvent.click(within(lines[0]!).getByRole("button", { name: /2 comments from illegalcall and whoisasx/ }));
		expect(lines[0]).toHaveTextContent("A failed turn-on does not undo what it started.");
		expect(lines[0]).toHaveTextContent("Rename this.");
		expect(lines[0]).toHaveTextContent("renderer-smoke");
	});

	it("says feedback the agent has not been sent yet waits for its turn, and gives a sent one its time", () => {
		draw(reviewComment, {}, true);
		const queued = screen.getByTestId("pull-request-feedback");
		expect(queued).toHaveTextContent("1 comment from illegalcall on #6974");
		expect(queued).toHaveTextContent("after this turn");
		expect(queued.querySelector("time")).toBeNull();
		cleanup();

		draw(reviewComment);
		const sent = screen.getByTestId("pull-request-feedback");
		expect(sent).not.toHaveTextContent("after this turn");
		expect(sent.querySelector("time")).toHaveAttribute("datetime", "2026-10-20T12:32:00Z");
	});

	it("keeps a human message that reads like a review nudge out of the burst", () => {
		const pasted = message(reviewComment, { id: "h1", origin: "human" });
		const first = message(reviewComment, { id: "m1" });
		const second = message(reviewComment, { id: "m2" });
		const bursts = pullRequestBursts([pasted, first, second]);
		expect(bursts.has("h1")).toBe(false);
		expect(bursts.get("m1")).toEqual([first, second]);
		expect(bursts.get("m2")).toBeNull();
	});

	it("times PR feedback by when it happened on the forge, not when AO delivered it", () => {
		draw(reviewComment.replace("need additional context.\n", "need additional context. Newest comment posted at 2026-11-16T07:54:42Z.\n"), { createdAt: "2026-11-16T08:26:00Z" });
		expect(screen.getByTestId("pull-request-feedback").querySelector("time")).toHaveAttribute("datetime", "2026-11-16T07:54:42Z");
		cleanup();

		// Messages from before the daemon wrote the time keep the delivery time.
		draw(reviewComment, { createdAt: "2026-11-16T08:26:00Z" });
		expect(screen.getByTestId("pull-request-feedback").querySelector("time")).toHaveAttribute("datetime", "2026-11-16T08:26:00Z");
	});

	it("ends a line in its newest instant when a forge time and a delivery time differ in fractional seconds", () => {
		const review = message(reviewComment.replace("need additional context.\n", "need additional context. Newest comment posted at 2026-11-26T12:00:00Z.\n"), { id: "m1" });
		const checks = message(ciFailing, { id: "m2", sequence: 2, createdAt: "2026-11-26T12:00:00.500Z" });
		const bursts = pullRequestBursts([review, checks]);
		render(<TooltipProvider><AutomationMessage message={review} burst={bursts.get("m1")} fallback={<p>card</p>} /></TooltipProvider> as ReactElement);
		expect(screen.getByTestId("pull-request-feedback").querySelector("time")).toHaveAttribute("datetime", "2026-11-26T12:00:00.500Z");
	});

	it("draws a review comment as who said what, and where", async () => {
		draw(reviewComment);

		const line = screen.getByTestId("pull-request-feedback");
		expect(line).toHaveTextContent("1 comment from illegalcall on #6974");
		await userEvent.click(within(line).getByRole("button", { name: /1 comment from illegalcall on #6974/ }));
		expect(line).toHaveTextContent("ThisComputerAccess.tsx:97");
		expect(line).toHaveTextContent("A failed turn-on does not undo what it started.");
		expect(line).not.toHaveTextContent("Thread ID");
	});

	it("draws a review comment's HTML the way GitHub does, without remote images", async () => {
		const body = [
			"<!-- coderabbit-ai-generated:v1 -->",
			"",
			'**<picture><img src="https://storage.googleapis.com/a/amber-16.svg" width="16" height="16" alt=""></picture> Minor** · **Quick win**',
			"",
			"Inserting the path into `'<root>'` breaks the query.",
			"",
			"![Flow diagram](https://example.com/flow.png)",
			"",
			"<details>",
			'<summary><picture><source media="(prefers-color-scheme: dark)" srcset="https://storage.googleapis.com/a/bot-dark.svg"><img src="https://storage.googleapis.com/a/bot.svg" alt=""></picture> Prompt for AI Agents</summary>',
			"<dl>",
			"<dd>",
			"",
			"```",
			"Bind the root as a parameter.",
			"```",
			"",
			"</dd>",
			"</dl>",
			"</details>",
			"",
			"<pre>first",
			"  second</pre>",
			"",
			"<pre>third<br>fourth</pre>",
			"",
			"<!-- This is an auto-generated comment by CodeRabbit -->",
		].join("\n");
		draw(reviewComment.replace("A failed turn-on does not undo what it started.\n\nThe switch then shows **off**.", body));

		const event = screen.getByTestId("pull-request-feedback");
		await userEvent.click(within(event).getByRole("button", { name: /1 comment from illegalcall on #6974/ }));
		expect(event).toHaveTextContent("Minor · Quick win");
		expect(event).toHaveTextContent("Inserting the path into '<root>' breaks the query.");
		expect(event).not.toHaveTextContent("<!--");
		expect(event).not.toHaveTextContent("<picture");
		expect(event).not.toHaveTextContent("storage.googleapis.com");
		expect(event).toHaveTextContent("Flow diagram");
		expect(event).not.toHaveTextContent("example.com/flow.png");
		// The only pictures are the reviewer's avatar, on the line and on the comment.
		expect(event.querySelector("picture, source")).toBeNull();
		expect([...event.querySelectorAll("img")].map((img) => img.getAttribute("src"))).toEqual(Array(2).fill("https://github.com/illegalcall.png?size=40"));
		const details = event.querySelector("details");
		expect(details).not.toHaveAttribute("open");
		expect(details?.querySelector("summary")).toHaveTextContent("Prompt for AI Agents");
		expect(details).toHaveTextContent("Bind the root as a parameter.");
		expect([...event.querySelectorAll("pre")].map((pre) => pre.textContent)).toContain("first\n  second");
		expect([...event.querySelectorAll("pre")].map((pre) => pre.textContent)).toContain("third\nfourth");

		await userEvent.click(within(details as HTMLElement).getByText("Prompt for AI Agents"));
		expect(details).toHaveAttribute("open");
		await userEvent.click(await within(event).findByRole("button", { name: "Show less" }));
		expect(details).not.toHaveAttribute("open");
	});
});
