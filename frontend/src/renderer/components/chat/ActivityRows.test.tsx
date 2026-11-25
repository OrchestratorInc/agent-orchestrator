import { fireEvent, render as rtlRender } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { ReactElement } from "react";
import { ActivityRow } from "./ChatTimelineItems";
import { ActivityRun } from "./ActivityRun";
import type { ConversationActivity } from "../../types/conversation";
import { TooltipProvider } from "../ui/tooltip";

/** Rows carry copy buttons, whose tooltips need the app's provider. */
function render(ui: ReactElement) {
	return rtlRender(<TooltipProvider>{ui}</TooltipProvider>);
}

function activity(overrides: Partial<ConversationActivity>): ConversationActivity {
	return {
		kind: "activity",
		id: "a1",
		sequence: 1,
		revision: 1,
		activityKind: "command",
		status: "completed",
		summary: "",
		createdAt: "2026-11-24T10:00:00Z",
		...overrides,
	} as ConversationActivity;
}

describe("step rows and their threads", () => {
	it("marks another agent's tool by its kind, and keeps the question mark for questions", () => {
		const edit = render(<ActivityRow activity={activity({ activityKind: "mcp_tool", summary: "Edit a.ts", detail: { protocol: "acp", toolKind: "edit", input: {} } } as Partial<ConversationActivity>)} />);
		expect(edit.container.querySelector(".icon-file-pen")).not.toBeNull();
		edit.unmount();
		const other = render(<ActivityRow activity={activity({ activityKind: "mcp_tool", summary: "list_issues", detail: { toolName: "list_issues", server: "linear", arguments: {} } } as Partial<ConversationActivity>)} />);
		expect(other.container.querySelector(".icon-wrench")).not.toBeNull();
		other.unmount();
		const question = render(<ActivityRow activity={activity({ activityKind: "mcp_tool", summary: "Where?", detail: { protocol: "acp", toolKind: "other", input: { questions: [{ question: "Where?" }] } } } as Partial<ConversationActivity>)} />);
		expect(question.container.querySelector(".icon-tool-question")).not.toBeNull();
	});

	it("names one read file in a chip with its icon, and leaves other targets plain", () => {
		const { unmount } = render(
			<ActivityRow activity={activity({ summary: "cat", detail: { command: "cat -n src/app.ts", output: "     1\ta", exitCode: 0 } })} />,
		);
		const chip = document.querySelector("[data-step-file-chip]");
		expect(chip).toHaveTextContent("app.ts");
		expect(chip?.querySelector("[data-file-icon]")).not.toBeNull();
		unmount();

		render(<ActivityRow activity={activity({ summary: "rg", detail: { command: "rg TODO src", output: "", exitCode: 1 } })} />);
		expect(document.querySelector("[data-step-file-chip]")).toBeNull();
	});

	it("hangs a run's steps on one thread and an opened step's box under its icon, with no line of its own", () => {
		const { container } = render(
			<ActivityRun
				activities={[
					activity({ id: "a", sequence: 1, summary: "rg", detail: { command: "rg TODO src", output: "src/a.ts:1: TODO", exitCode: 0 } }),
					activity({ id: "b", sequence: 2, summary: "npm", detail: { command: "npm test", output: "ok", exitCode: 0 } }),
				]}
			/>,
		);
		fireEvent.click(container.querySelector(".activity-run-toggle")!);
		expect(container.querySelectorAll("[data-activity-rail]")).toHaveLength(1);
		fireEvent.click(container.querySelector('.activity-row-toggle[aria-expanded="false"]')!);
		const rails = container.querySelectorAll("[data-activity-rail]");
		expect(rails).toHaveLength(1);
		expect(rails[0]).toHaveClass("activity-thread");
		const line = container.querySelector("[data-command-line]");
		expect(line).not.toBeNull();
		expect(line?.closest(".pl-\\[22px\\]")).toBeNull();
	});

	it("draws a rail under a step opened on its own", () => {
		const { container } = render(
			<ActivityRow activity={activity({ summary: "npm", detail: { command: "npm test", output: "ok", exitCode: 0 } })} />,
		);
		expect(container.querySelector("[data-activity-rail]")).toBeNull();
		fireEvent.click(container.querySelector(".activity-row-toggle")!);
		// Titled with its command, it opens straight onto the Shell card, no $ line again.
		expect(container.querySelector("[data-activity-rail] [data-command-shell]")).not.toBeNull();
		expect(container.querySelector("[data-command-line]")).toBeNull();
	});

	it("marks a run with the kind of step it holds most of", () => {
		const { container, unmount } = render(
			<ActivityRun
				activities={[
					activity({ id: "a", sequence: 1, summary: "node", detail: { command: "node --version", output: "v22", exitCode: 0 } }),
					activity({ id: "b", sequence: 2, summary: "uname", detail: { command: "uname -a", output: "Darwin", exitCode: 0 } }),
					activity({ id: "c", sequence: 3, summary: "cat", detail: { command: "cat a.ts", output: "x", exitCode: 0 } }),
				]}
			/>,
		);
		expect(container.querySelector("[data-run-kind]")).toHaveAttribute("data-run-kind", "execute");
		unmount();

		const reads = render(
			<ActivityRun
				activities={[
					activity({ id: "d", sequence: 4, summary: "cat", detail: { command: "cat a.ts", output: "x", exitCode: 0 } }),
					activity({ id: "e", sequence: 5, summary: "cat", detail: { command: "cat b.ts", output: "y", exitCode: 0 } }),
				]}
			/>,
		);
		expect(reads.container.querySelector("[data-run-kind]")).toHaveAttribute("data-run-kind", "read");
	});

	it("hangs an edit's list of files on its own rail inside a run", () => {
		const { container } = render(
			<ActivityRun
				activities={[
					activity({ id: "a", sequence: 1, summary: "npm", detail: { command: "npm test", output: "ok", exitCode: 0 } }),
					activity({
						id: "b",
						sequence: 2,
						activityKind: "file_change",
						summary: "Edit demo/math.js",
						detail: {
							files: [
								{ path: "demo/math.js", status: "modified", additions: 1, deletions: 1, patch: "@@ -1 +1 @@\n-a\n+b" },
								{ path: "demo/math.test.js", status: "modified", additions: 1, deletions: 1, patch: "@@ -1 +1 @@\n-c\n+d" },
							],
						},
					}),
				]}
			/>,
		);
		fireEvent.click(container.querySelector(".activity-run-toggle")!);
		const edit = [...container.querySelectorAll(".activity-row-toggle")].find((row) => /Edit/.test(row.textContent ?? ""))!;
		fireEvent.click(edit);
		const rails = container.querySelectorAll("[data-activity-rail]");
		expect(rails).toHaveLength(2);
		expect(rails[1]!.querySelectorAll("li")).toHaveLength(2);
	});

	it("keeps a row's file chip neutral: it opens nothing, so it takes no file tint", () => {
		const { container } = render(
			<ActivityRow activity={activity({ summary: "cat", detail: { command: "cat -n src/app.ts", output: "     1\ta", exitCode: 0 } })} />,
		);
		const chip = container.querySelector("[data-step-file-chip]")!;
		expect(chip.className).not.toMatch(/chat-file/);
	});

	it("starts a nested edit's diff where its file row's text starts", () => {
		const { container } = render(
			<ActivityRow
				activity={activity({
					activityKind: "file_change",
					summary: "Edit demo/math.js",
					detail: {
						files: [
							{ path: "demo/math.js", status: "modified", additions: 1, deletions: 1, patch: "@@ -1 +1 @@\n-a\n+b" },
							{ path: "demo/math.js", status: "modified", additions: 1, deletions: 1, patch: "@@ -5 +5 @@\n-c\n+d" },
						],
					},
				})}
			/>,
		);
		fireEvent.click(container.querySelector(".activity-row-toggle")!);
		fireEvent.click(container.querySelectorAll("li button")[0]!);
		expect(container.querySelectorAll("li button")[0]).toHaveClass("pl-2.5");
		// Its patch sits under the row's text too.
		const patch = container.querySelector("[data-tool-diff-row]")!;
		expect(patch.closest(".pl-2\\.5:not(button)")).not.toBeNull();
	});

	it("branches straight into a middle step and curves only into the last", () => {
		const { container } = render(
			<ActivityRun
				activities={[
					activity({ id: "a", sequence: 1, summary: "rg", detail: { command: "rg TODO src", output: "x", exitCode: 0 } }),
					activity({ id: "b", sequence: 2, summary: "npm", detail: { command: "npm test", output: "ok", exitCode: 0 } }),
					activity({ id: "c", sequence: 3, summary: "ls", detail: { command: "ls -la", output: "y", exitCode: 0 } }),
				]}
			/>,
		);
		fireEvent.click(container.querySelector(".activity-run-toggle")!);
		const steps = [...container.querySelector(".activity-thread")!.children];
		// jsdom does not lay out pseudo-elements; the rule keys on being the last child.
		expect(steps).toHaveLength(3);
		expect(steps.at(-1)!.matches(":last-child")).toBe(true);
		expect(steps.slice(0, -1).every((step) => !step.matches(":last-child"))).toBe(true);
	});
});
