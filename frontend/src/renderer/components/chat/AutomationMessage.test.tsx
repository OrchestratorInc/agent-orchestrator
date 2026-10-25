/**
 * AO's automation messages are read back from the text AO writes for them. The
 * templates this renderer writes are produced here by their real formatters, so
 * a change to one fails the reader it breaks; the daemon's are fixed samples of
 * lifecycle's formatters.
 */

import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactElement } from "react";
import { describe, expect, it } from "vitest";
import { formatDiffSelectionMessage } from "../../../shared/diff-selection";
import { formatFileAnnotationMessage, formatFileAnnotationMessages } from "../../../shared/file-annotations";
import type { ConversationMessage } from "../../types/conversation";
import { gitActionMessage } from "../SessionBranchSummary";
import { TooltipProvider } from "../ui/tooltip";
import { AutomationMessage, readAutomation } from "./AutomationMessage";

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
	"Address the requested changes and push. You should not need to re-fetch the review unless you need additional context beyond what AO has provided here.",
].join("\n");

const mergeConflict = `There are merge conflicts on ${PR}. Rebase onto the base branch and resolve them.\nPR: ${URL}`;

function message(text: string): ConversationMessage {
	return { kind: "message", id: "m1", sequence: 1, revision: 0, role: "user", origin: "automation", text, streaming: false, createdAt: "2026-10-20T12:32:00Z" };
}

function draw(text: string) {
	return render(<TooltipProvider><AutomationMessage message={message(text)} fallback={<p>card</p>} /></TooltipProvider> as ReactElement);
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
			comments: [{ path: "a.ts", line: 3, feedback: "rename this" }, { path: "b.ts", line: undefined, feedback: "split this file" }],
		});
	});

	it("reads a diff selection and the git buttons' instructions", () => {
		const selection = formatDiffSelectionMessage({
			instruction: "Use a constant here.",
			filePath: "src/app.ts",
			lines: [{ kind: "add", oldNo: null, newNo: 10, text: "a" }, { kind: "add", oldNo: null, newNo: 12, text: "b" }],
		});
		expect(readAutomation(selection)).toEqual({ kind: "diffSelection", path: "src/app.ts", start: 10, end: 12, instruction: "Use a constant here." });

		expect(readAutomation(gitActionMessage("createPR", "main", undefined))).toEqual({ kind: "gitAction", action: "createPR", base: "main" });
		expect(readAutomation(gitActionMessage("createDraftPR", undefined, undefined))).toEqual({ kind: "gitAction", action: "createDraftPR", base: undefined });
		expect(readAutomation(gitActionMessage("commit", "main", undefined))).toEqual({ kind: "gitAction", action: "commit" });
		expect(readAutomation(gitActionMessage("commitPush", "main", 12))).toEqual({ kind: "gitAction", action: "commitPush" });
		expect(readAutomation(gitActionMessage("push", "main", undefined))).toEqual({ kind: "gitAction", action: "push" });
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

	it("draws failing CI on the PR, with each check's log a click away", async () => {
		draw(ciFailing);

		const event = screen.getByTestId("pull-request-event");
		expect(event).toHaveTextContent("CI failing on #6974");
		expect(within(event).getByRole("link", { name: "#6974" })).toHaveAttribute("href", URL);
		expect(within(event).getByRole("link", { name: "renderer-smoke" })).toHaveAttribute("href", "https://github.com/o/r/actions/runs/1/job/2");
		expect(event).not.toHaveTextContent("npm ERR!");

		await userEvent.click(within(event).getByRole("button", { name: "Show log" }));
		expect(event).toHaveTextContent("npm ERR! smoke failed");
	});

	it("draws a review comment as who said what, and where", () => {
		draw(reviewComment);

		const event = screen.getByTestId("pull-request-event");
		expect(event).toHaveTextContent("@illegalcall commented on #6974");
		expect(event).toHaveTextContent("ThisComputerAccess.tsx:97");
		expect(event).toHaveTextContent("A failed turn-on does not undo what it started.");
		expect(event).not.toHaveTextContent("Thread ID");
	});

	it("keeps the card for anything it cannot read", () => {
		draw("Checks failed:\nlint exited 1");

		expect(screen.getByText("card")).toBeInTheDocument();
	});
});
