import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { ConversationActivity } from "../../types/conversation";
import { TooltipProvider } from "../ui/tooltip";
import { ActivityRun } from "./ActivityRun";

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

describe("ActivityRun in a running turn", () => {
	const calls = [
		tool("activity-1", "tool-a", "Read package.json"),
		tool("activity-2", "tool-b", "Search for SEC-164"),
	];

	it("shows each call while the turn runs and folds once it settles", async () => {
		const { rerender } = render(<ActivityRun activities={calls} live />);
		expect(screen.getByRole("button", { name: /Ran 2 tool calls/i })).toHaveAttribute("aria-expanded", "true");
		expect(screen.getByText("Read package.json")).toBeInTheDocument();
		expect(screen.getByText("Search for SEC-164")).toBeInTheDocument();

		rerender(<ActivityRun activities={calls} live={false} />);
		expect(screen.getByRole("button", { name: /Ran 2 tool calls/i })).toHaveAttribute("aria-expanded", "false");
		await waitFor(() => expect(screen.queryByText("Read package.json")).not.toBeInTheDocument());
	});

	it("keeps the reader's choice over the live default", async () => {
		const user = userEvent.setup();
		const { rerender } = render(<ActivityRun activities={calls} live />);
		await user.click(screen.getByRole("button", { name: /Ran 2 tool calls/i }));
		await waitFor(() => expect(screen.queryByText("Read package.json")).not.toBeInTheDocument());

		rerender(<ActivityRun activities={calls} live={false} />);
		await user.click(screen.getByRole("button", { name: /Ran 2 tool calls/i }));
		rerender(<ActivityRun activities={calls} live />);
		rerender(<ActivityRun activities={calls} live={false} />);
		expect(screen.getByText("Read package.json")).toBeInTheDocument();
	});

	it("shows a working subagent's steps while the turn runs", () => {
		render(
			<ActivityRun
				live
				activities={[
					tool("activity-1", "agent-tool", "Delegate repository scan"),
					{ ...tool("activity-2", "child-tool", "Search source files", "agent-tool"), status: "running" },
				]}
			/>,
		);
		expect(screen.getByRole("button", { name: /Subagent 1 step/i })).toHaveAttribute("aria-expanded", "true");
		expect(screen.getByText("Search source files")).toBeInTheDocument();
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
		expect(screen.getByRole("button", { name: /Explored 2 tool calls/i })).toBeInTheDocument();
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

		await user.click(screen.getByRole("button", { name: /Explored 2 tool calls/i }));
		expect(await screen.findByRole("button", { name: "Read README.md" })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Searched the web for latest stable Node.js version" })).toBeInTheDocument();
		expect(container.querySelector(".lucide-globe")).toHaveClass("size-3");
		expect(screen.queryByText('"latest stable Node.js version"')).not.toBeInTheDocument();
	});
});
