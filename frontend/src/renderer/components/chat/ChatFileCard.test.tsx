import { fireEvent, render as rtlRender, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { ReactElement } from "react";
import { ActivityRow } from "./ChatTimelineItems";
import { ActivityRun } from "./ActivityRun";
import { chatDiff, fileCardPath, readFileLines } from "./ChatFileCard";
import type { ConversationActivity } from "../../types/conversation";
import { TooltipProvider } from "../ui/tooltip";

function render(ui: ReactElement) {
	return rtlRender(<TooltipProvider>{ui}</TooltipProvider>);
}

const root = "/Users/me/.ao/data/worktrees/shop/shop-2";

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

describe("chatDiff", () => {
	it("keeps a hunk's own line numbers", () => {
		const { rows, numbered } = chatDiff("--- a/x.ts\n+++ b/x.ts\n@@ -38,3 +38,4 @@\n a\n-b\n+c\n+d\n e");
		expect(numbered).toBe(true);
		expect(rows.filter((row) => row.kind !== "hunk").map((row) => [row.kind, row.oldNo, row.newNo])).toEqual([
			["context", 38, 38],
			["del", 39, null],
			["add", null, 39],
			["add", null, 40],
			["context", 40, 41],
		]);
	});

	it("leaves an edit without a hunk range unnumbered", () => {
		const { rows, numbered } = chatDiff(`--- ${root}/a.yaml\n+++ ${root}/a.yaml\n-replicas: 1\n+replicas: 3`);
		expect(numbered).toBe(false);
		expect(rows.filter((row) => row.kind !== "hunk").map((row) => [row.kind, row.text, row.oldNo, row.newNo])).toEqual([
			["del", "replicas: 1", null, null],
			["add", "replicas: 3", null, null],
		]);
	});

	it("numbers a new file from 1 and drops its trailing newline", () => {
		const { rows, numbered } = chatDiff(`--- ${root}/a.sh\n+++ ${root}/a.sh\n+#!/bin/sh\n+echo hi\n+`, "added");
		expect(numbered).toBe(true);
		expect(rows.filter((row) => row.kind === "add").map((row) => [row.newNo, row.text])).toEqual([
			[1, "#!/bin/sh"],
			[2, "echo hi"],
		]);
	});
});

describe("readFileLines", () => {
	it("takes Read's and cat -n's numbers and drops a reminder", () => {
		expect(readFileLines("1\tone\n2\t\n     3\tthree\n<system-reminder>x</system-reminder>\n")?.lines).toEqual([
			{ no: 1, text: "one" },
			{ no: 2, text: "" },
			{ no: 3, text: "three" },
		]);
	});

	it("counts a plain copy from its first line", () => {
		expect(readFileLines("a\nb", 10)?.lines).toEqual([{ no: 10, text: "a" }, { no: 11, text: "b" }]);
	});
});

describe("fileCardPath", () => {
	it("shows the path from the workspace root", () => {
		expect(fileCardPath(`${root}/src/lib/money.ts`, { cwd: root })).toBe("src/lib/money.ts");
		expect(fileCardPath(`${root}/src/lib/money.ts`, { summary: "Read src/lib/money.ts" })).toBe("src/lib/money.ts");
		expect(fileCardPath(`${root}/README.md`, { summary: "Read README.md (1 - 40)" })).toBe("README.md");
		expect(fileCardPath(`${root}/src/a.ts`)).toBe("src/a.ts");
		expect(fileCardPath("./src/a.ts")).toBe("src/a.ts");
		expect(fileCardPath("/Users/me/notes/a.md")).toBe("~/notes/a.md");
	});
});

describe("file cards in the timeline", () => {
	it("opens an edit onto the file's diff, named by its workspace path", () => {
		render(
			<ActivityRow
				activity={activity({
					activityKind: "file_change",
					summary: "Edit scratch/a.yaml",
					detail: {
						files: [{
							path: `${root}/scratch/a.yaml`,
							status: "modified",
							additions: 1,
							deletions: 1,
							patch: `--- ${root}/scratch/a.yaml\n+++ ${root}/scratch/a.yaml\n-replicas: 1\n+replicas: 3`,
						}],
					},
				})}
			/>,
		);
		fireEvent.click(screen.getByRole("button", { name: /a\.yaml/ }));
		const card = document.querySelector('[data-chat-file-card="edit"]');
		expect(card).toHaveTextContent("scratch/a.yaml");
		expect(card?.querySelectorAll('[data-kind="del"]')).toHaveLength(1);
		expect(card?.querySelectorAll('[data-kind="add"]')).toHaveLength(1);
		expect(screen.getByRole("button", { name: "Copy diff" })).toBeInTheDocument();
	});

	it("opens Claude Code's Read onto the file it returned", () => {
		render(
			<ActivityRow
				activity={activity({
					activityKind: "mcp_tool",
					summary: "Read src/lib/money.ts",
					detail: {
						protocol: "acp",
						providerToolName: "Read",
						toolKind: "read",
						input: { file_path: `${root}/src/lib/money.ts` },
						output: "1\texport const one = 1;\n2\texport const two = 2;",
					},
				} as Partial<ConversationActivity>)}
			/>,
		);
		fireEvent.click(screen.getByRole("button", { name: /money\.ts/ }));
		const card = document.querySelector('[data-chat-file-card="read"]');
		expect(card).toHaveTextContent("src/lib/money.ts");
		expect(card).toHaveTextContent("export const two = 2;");
		expect(screen.queryByText("Arguments")).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Copy file" })).toBeInTheDocument();
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

	it("shows sed -n's lines numbered from where it started, and a piped read as a command", () => {
		const { unmount } = render(
			<ActivityRow
				activity={activity({
					summary: "sed",
					detail: { command: "/bin/zsh -lc \"sed -n '20,21p' src/app.ts\"", output: "const a = 1;\nconst b = 2;\n", exitCode: 0 },
				})}
			/>,
		);
		fireEvent.click(screen.getByRole("button", { name: /app\.ts/ }));
		const card = document.querySelector('[data-chat-file-card="read"]');
		expect(card).toHaveTextContent("20const a = 1;21const b = 2;");
		unmount();

		render(
			<ActivityRow
				activity={activity({
					summary: "cat",
					detail: { command: "cat src/app.ts | head", output: "const a = 1;\n", exitCode: 0 },
				})}
			/>,
		);
		fireEvent.click(screen.getAllByRole("button")[0]!);
		expect(document.querySelector("[data-chat-file-card]")).toBeNull();
		// The command is one $ line with its own copy; the output sits in a Shell card.
		expect(document.querySelector("[data-command-line]")).toHaveTextContent("$ cat src/app.ts | head");
		expect(screen.getByRole("button", { name: "Copy command" })).toBeInTheDocument();
		expect(document.querySelector("[data-command-shell]")).toHaveTextContent("Shell");
		expect(screen.getByRole("button", { name: "Copy output" })).toBeInTheDocument();
	});

	it("hangs a run's steps on one rail and an opened step on its text column, not a second rail", () => {
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
		expect(container.querySelectorAll("[data-activity-rail]")).toHaveLength(1);
		expect(container.querySelector("[data-command-line]")?.closest(".pl-\\[22px\\]")).not.toBeNull();
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

	it("indents a nested edit's diff under its row instead of lining it up with the row's text", () => {
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
		const card = container.querySelector('[data-chat-file-card="edit"]')!;
		expect(card.parentElement).toHaveClass("pl-[22px]");
	});
});
