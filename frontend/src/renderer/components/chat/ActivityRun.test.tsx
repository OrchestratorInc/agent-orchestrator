import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { ConversationActivity } from "../../types/conversation";
import { TooltipProvider } from "../ui/tooltip";
import { ActivityRun } from "./ActivityRun";
import { RevealedChatActivity } from "./ChatTimelineItems";

function tool(
	id: string,
	providerItemId: string,
	summary: string,
	parentProviderItemId?: string,
): ConversationActivity {
	return {
		kind: "activity",
		id,
		providerItemId,
		sequence: Number(id.replace(/\D/g, "")),
		revision: 0,
		activityKind: "mcp_tool",
		status: "completed",
		summary,
		detail: { parentProviderItemId },
		createdAt: "2026-08-04T00:00:00Z",
	};
}

describe("ActivityRun nested agents", () => {
	it("keeps child-agent work under its parent and collapsed by default", async () => {
		const user = userEvent.setup();
		render(
			<ActivityRun
				activities={[
					tool("activity-1", "agent-tool", "Delegate repository scan"),
					tool("activity-2", "child-tool", "Search source files", "agent-tool"),
				]}
			/>,
		);

		await user.click(screen.getByRole("button", { name: /Ran 2 tool calls/i }));
		expect(await screen.findByRole("button", { name: /Subagent 1 step/i })).toHaveAttribute(
			"aria-expanded",
			"false",
		);
		expect(screen.queryByText("Search source files")).not.toBeInTheDocument();

		await user.click(screen.getByRole("button", { name: /Subagent 1 step/i }));
		expect(await screen.findByText("Search source files")).toBeInTheDocument();
	});

	// A search match in a subagent's call: every fold above it opens, the
	// subagent's included, so the matched row mounts and can be marked.
	it("opens every fold above a search match inside a subagent", async () => {
		render(
			<RevealedChatActivity.Provider value="activity-2">
				<ActivityRun
					activities={[
						tool("activity-1", "agent-tool", "Delegate repository scan"),
						tool("activity-2", "child-tool", "Search source files", "agent-tool"),
					]}
				/>
			</RevealedChatActivity.Provider>,
		);

		expect(await screen.findByText("Search source files")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: /Subagent 1 step/i })).toHaveAttribute("aria-expanded", "true");
		expect(document.querySelector('[data-chat-activity-id="activity-2"]')).not.toBeNull();
	});

	// The reader closed the subagent after one match, then picked another in it:
	// the fold must open again for the new row.
	it("reopens a subagent the reader closed when another match in it is revealed", async () => {
		const user = userEvent.setup();
		const activities = [
			tool("activity-1", "agent-tool", "Delegate repository scan"),
			tool("activity-2", "child-tool", "Search source files", "agent-tool"),
			tool("activity-3", "child-tool-2", "Read the config file", "agent-tool"),
		];
		const { rerender } = render(
			<RevealedChatActivity.Provider value="activity-2"><ActivityRun activities={activities} /></RevealedChatActivity.Provider>,
		);
		const subagent = await screen.findByRole("button", { name: /Subagent 2 steps/i });
		expect(subagent).toHaveAttribute("aria-expanded", "true");
		await user.click(subagent);
		expect(subagent).toHaveAttribute("aria-expanded", "false");

		rerender(<RevealedChatActivity.Provider value="activity-3"><ActivityRun activities={activities} /></RevealedChatActivity.Provider>);
		await waitFor(() => expect(screen.getByRole("button", { name: /Subagent 2 steps/i })).toHaveAttribute("aria-expanded", "true"));
		expect(await screen.findByText("Read the config file")).toBeInTheDocument();
	});

	it("keeps malformed provider cycles visible at the top level", async () => {
		const user = userEvent.setup();
		render(
			<ActivityRun
				activities={[
					tool("activity-1", "tool-a", "First", "tool-b"),
					tool("activity-2", "tool-b", "Second", "tool-a"),
				]}
			/>,
		);
		await user.click(screen.getByRole("button", { name: /Ran 2 tool calls/i }));
		expect(await screen.findByText("First")).toBeInTheDocument();
		expect(await screen.findByText("Second")).toBeInTheDocument();
	});
});

describe("ActivityRun disclosure", () => {
	const calls = [
		tool("activity-1", "tool-a", "Read package.json"),
		tool("activity-2", "tool-b", "Search for SEC-164"),
	];
	const run = () => screen.getByRole("button", { name: /Ran 2 tool calls/i });

	it("stays folded while a call in it runs and prints", () => {
		const printing: ConversationActivity = {
			...calls[0]!,
			status: "running",
			detail: { output: "working\n" },
		};
		render(<ActivityRun activities={[printing, calls[1]!]} />);
		expect(run()).toHaveAttribute("aria-expanded", "false");
		expect(screen.queryByText("Read package.json")).not.toBeInTheDocument();
		expect(screen.queryByText("Search for SEC-164")).not.toBeInTheDocument();
	});

	it("opens and closes only when the reader asks", async () => {
		const user = userEvent.setup();
		render(<ActivityRun activities={calls} />);
		await user.click(run());
		expect(run()).toHaveAttribute("aria-expanded", "true");
		expect(await screen.findByText("Read package.json")).toBeInTheDocument();

		await user.click(run());
		expect(run()).toHaveAttribute("aria-expanded", "false");
		await waitFor(() => expect(screen.queryByText("Read package.json")).not.toBeInTheDocument());
	});

	it("opens from the keyboard", async () => {
		const user = userEvent.setup();
		render(<ActivityRun activities={calls} />);
		run().focus();
		await user.keyboard("{Enter}");
		expect(run()).toHaveAttribute("aria-expanded", "true");
	});

	it("keeps a working subagent's steps folded", async () => {
		const user = userEvent.setup();
		render(
			<ActivityRun
				activities={[
					tool("activity-1", "agent-tool", "Delegate repository scan"),
					{ ...tool("activity-2", "child-tool", "Search source files", "agent-tool"), status: "running" },
				]}
			/>,
		);
		await user.click(run());
		expect(await screen.findByRole("button", { name: /Subagent 1 step/i })).toHaveAttribute("aria-expanded", "false");
		expect(screen.queryByText("Search source files")).not.toBeInTheDocument();
	});

	it("keeps a failed call on screen while its run is folded", async () => {
		const user = userEvent.setup();
		render(<ActivityRun activities={[calls[0]!, { ...calls[1]!, status: "failed" }]} />);
		expect(run()).toHaveAttribute("aria-expanded", "false");
		expect(screen.queryByText("Read package.json")).not.toBeInTheDocument();
		expect(screen.getByText("Search for SEC-164")).toBeInTheDocument();

		// Opening the run puts the failed call back in its place, once.
		await user.click(run());
		expect(await screen.findByText("Read package.json")).toBeInTheDocument();
		await waitFor(() => expect(screen.getAllByText("Search for SEC-164")).toHaveLength(1));
	});

	it("keeps a call the tool server rejected on screen, even when its status says completed", () => {
		render(<ActivityRun activities={[calls[0]!, { ...calls[1]!, detail: { success: false } }]} />);
		expect(screen.getByText("Search for SEC-164")).toBeInTheDocument();
	});

	it("keeps a failed subagent step on screen while the subagent is folded", async () => {
		const user = userEvent.setup();
		render(
			<ActivityRun
				activities={[
					tool("activity-1", "agent-tool", "Delegate repository scan"),
					{ ...tool("activity-2", "child-tool", "Search source files", "agent-tool"), status: "failed" },
				]}
			/>,
		);
		expect(screen.getByText("Search source files")).toBeInTheDocument();

		await user.click(run());
		expect(await screen.findByRole("button", { name: /Subagent 1 step/i })).toHaveAttribute("aria-expanded", "false");
		await waitFor(() => expect(screen.getAllByText("Search source files")).toHaveLength(1));
	});

	it("leaves an ordinary non-zero exit folded, counted on the summary line", () => {
		const exited = (id: string, line: string, exitCode: number): ConversationActivity => ({
			...tool(id, id, line), activityKind: "command", status: "failed", detail: { command: line, exitCode },
		});
		render(
			<TooltipProvider>
				<ActivityRun activities={[exited("activity-1", "grep -q a b", 1), exited("activity-2", "cat missing", 1)]} />
			</TooltipProvider>,
		);
		expect(screen.getByText("2 exited")).toBeInTheDocument();
		expect(screen.getAllByRole("button")).toHaveLength(1);
	});
});

describe("ActivityRun command groups", () => {
	function command(id: string, line: string): ConversationActivity {
		return { ...tool(id, id, line), activityKind: "command", detail: { command: line } };
	}

	it("groups a command by the program its row names, past cd and env assignments", () => {
		render(
			<TooltipProvider>
				<ActivityRun activities={[command("activity-1", "cd repo && rg TODO ."), command("activity-2", "FOO=1 cat a.ts")]} />
			</TooltipProvider>,
		);
		expect(screen.getByRole("button", { name: /Searched for 1 pattern · Read 1 file/i })).toBeInTheDocument();
	});
});

describe("ActivityRun agent built-in tools", () => {
	function acpTool(id: string, summary: string, providerToolName: string, toolKind: string, input: unknown): ConversationActivity {
		return {
			...tool(id, id, summary),
			detail: { protocol: "acp", providerToolName, toolKind, input } as ConversationActivity["detail"],
		};
	}

	it("names Claude Code's reads and web searches by what they did", async () => {
		const user = userEvent.setup();
		const { container } = render(
			<TooltipProvider>
				<ActivityRun
					activities={[
						acpTool("activity-1", "Read README.md (1 - 40)", "Read", "read", { file_path: "/w/README.md", limit: 40 }),
						acpTool("activity-2", '"latest stable Node.js version"', "WebSearch", "fetch", { query: "latest stable Node.js version" }),
					]}
				/>
			</TooltipProvider>,
		);

		await user.click(screen.getByRole("button", { name: /Read 1 file · Searched the web/i }));
		expect(await screen.findByRole("button", { name: "Read README.md" })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Searched the web for latest stable Node.js version" })).toBeInTheDocument();
		expect(container.querySelector(".icon-globe")).toHaveClass("size-3");
		expect(screen.queryByText('"latest stable Node.js version"')).not.toBeInTheDocument();
	});
});
