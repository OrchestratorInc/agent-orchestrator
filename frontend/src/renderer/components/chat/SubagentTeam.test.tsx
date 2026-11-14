import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { ConversationActivity } from "../../types/conversation";
import {
	subagentCommand as command,
	subagentHandback as handback,
	subagentLaunch as launch,
} from "../../test/subagent-fixtures";
import { OpenSubagentContext, SubagentTeam } from "./SubagentTeam";

function team(live = true) {
	const pages = launch("pages", "Audit: info pages");
	const tests = launch("tests", "Audit: testing gaps");
	const steps = new Map<string, ConversationActivity[]>([
		["pages", [command("p1", "tool-pages", "cd /repo/app; grep -rn shipping src/app", "running")]],
		[
			"tests",
			[
				command("t1", "tool-tests", "cd /repo/app; git ls-files"),
				handback("t2", "tool-tests", "**Ranked 5 tests** to write first.\n\n1. Checkout totals"),
			],
		],
	]);
	return render(<SubagentTeam launches={[pages, tests]} steps={steps} live={new Set(live ? ["pages", "tests"] : [])} harness="claude-code" />);
}

/** The team's list starts closed; its header opens it. */
function openTeam() {
	fireEvent.click(screen.getByRole("button", { name: /^\d+ subagents?/ }));
}

describe("SubagentTeam", () => {
	it("summarizes the team and lists every child with its current state", () => {
		team();

		const header = screen.getByRole("button", { name: /2 subagents/ });
		// Closed until opened, working or not; the header counts what has ended, not who works.
		expect(header).toHaveAttribute("aria-expanded", "false");
		expect(within(header).queryByText(/working/)).not.toBeInTheDocument();
		expect(within(header).getByText("1 done")).toBeInTheDocument();
		openTeam();
		expect(header).toHaveAttribute("aria-expanded", "true");

		const working = screen.getByRole("button", { name: /Audit: info pages/ });
		expect(within(working).getByText("Explore")).toBeInTheDocument();
		expect(within(working).getByText("grep -rn shipping src/app")).toBeInTheDocument();
		expect(within(working).getByText(/Running/)).toBeInTheDocument();
		expect(within(working).getByLabelText("Working")).toBeInTheDocument();

		// A finished child shows the first line of its report, as a sentence.
		const done = screen.getByRole("button", { name: /Audit: testing gaps/ });
		expect(within(done).getByText("Ranked 5 tests to write first.")).toBeInTheDocument();
		// The report and the header's count already say it finished: no check on the
		// row, only the word for screen readers.
		expect(within(done).getByText("Done")).toHaveClass("sr-only");
		expect(within(done).queryByLabelText("Working")).not.toBeInTheDocument();
		expect(within(done).getByText("12s")).toBeInTheDocument();
	});

	it("names a specialized agent type but not Claude's default one", () => {
		const readme = launch("readme", "README audit");
		readme.detail = {
			...readme.detail,
			input: { description: "README audit", prompt: "Read-only. README audit.", subagent_type: "general-purpose" },
		} as ConversationActivity["detail"];
		render(<SubagentTeam launches={[readme, launch("pages", "Audit: info pages")]} steps={new Map()} live={new Set(["readme", "pages"])} harness="claude-code" />);
		openTeam();

		expect(within(screen.getByRole("button", { name: /README audit/ })).queryByText("general-purpose")).not.toBeInTheDocument();
		expect(within(screen.getByRole("button", { name: /Audit: info pages/ })).getByText("Explore")).toBeInTheDocument();
	});

	it("never shows the provider's internal launch note", () => {
		team();
		openTeam();
		expect(screen.queryByText(/Async agent launched/)).not.toBeInTheDocument();
		expect(screen.queryByText(/agentId/)).not.toBeInTheDocument();
	});

	it("opens a child inline onto its prompt, its steps and its report where the chat has no panel", async () => {
		const user = userEvent.setup();
		team();
		openTeam();

		const done = screen.getByRole("button", { name: /Audit: testing gaps/ });
		expect(done).toHaveAttribute("aria-expanded", "false");
		await user.click(done);
		expect(done).toHaveAttribute("aria-expanded", "true");

		expect(await screen.findByText("Report")).toBeInTheDocument();
		expect(screen.getByText("Checkout totals")).toBeInTheDocument();
		expect(screen.getByText("Sent by the parent agent")).toBeInTheDocument();
		expect(screen.getByText("Read-only. Audit: testing gaps.")).toBeInTheDocument();
		// The child's one command renders as an ordinary activity row.
		expect(screen.getByText("Checked repository")).toBeInTheDocument();
	});

	it("opens a child in the chat's Subagents panel instead of inline when there is one", async () => {
		const user = userEvent.setup();
		const openSubagent = vi.fn();
		render(
			<OpenSubagentContext.Provider value={openSubagent}>
				<SubagentTeam launches={[launch("tests", "Audit: testing gaps")]} steps={new Map()} live={new Set(["tests"])} harness="claude-code" />
			</OpenSubagentContext.Provider>,
			);
			openTeam();

			const row = screen.getByRole("button", { name: /Audit: testing gaps/ });
		// It goes somewhere else, so it is not a disclosure.
		expect(row).not.toHaveAttribute("aria-expanded");
		await user.click(row);

		expect(openSubagent).toHaveBeenCalledWith("tests");
		expect(screen.queryByText("Sent by the parent agent")).not.toBeInTheDocument();
	});

	it("opens and folds the list from its header", async () => {
	const user = userEvent.setup();
	team();
	const header = screen.getByRole("button", { name: /2 subagents/ });
	await user.click(header);
	expect(header).toHaveAttribute("aria-expanded", "true");
	expect(screen.getByRole("button", { name: /Audit: info pages/ })).toBeInTheDocument();
	await user.click(header);
	expect(header).toHaveAttribute("aria-expanded", "false");
		await waitFor(() =>
			expect(screen.queryByRole("button", { name: /Audit: info pages/ })).not.toBeInTheDocument(),
		);
	});

	it("states a failed launch in words, not color alone", () => {
		render(
			<SubagentTeam
				launches={[launch("over", "Audit: cart", "failed")]}
				steps={new Map()}
				live={new Set(["over"])}
				harness="claude-code"
				/>,
				);
				openTeam();
				const row = screen.getByRole("button", { name: /Audit: cart/ });
		expect(within(row).getByText("Failed")).toBeInTheDocument();
		expect(within(row).getByText(/Concurrent subagent limit reached/)).toBeInTheDocument();
		expect(within(screen.getByRole("button", { name: /1 subagent/ })).getByText("1 failed")).toBeInTheDocument();
	});

	it("keeps a child working after its turn settles while one of its steps still runs", () => {
		team(false);
		openTeam();
		const row = screen.getByRole("button", { name: /Audit: info pages/ });
		expect(within(row).getByLabelText("Working")).toBeInTheDocument();
	});

	it("says a background child gave no report once its turn has settled", () => {
		const quiet = launch("quiet", "Audit: admin");
		render(
			<SubagentTeam
				launches={[quiet]}
				steps={new Map([["quiet", [command("q1", "tool-quiet", "ls")]]])}
				live={new Set()}
				harness="claude-code"
				/>,
				);
				openTeam();
				const row = screen.getByRole("button", { name: /Audit: admin/ });
		expect(within(row).getByText("No report")).toBeInTheDocument();
		expect(within(row).queryByLabelText("Working")).not.toBeInTheDocument();
	});

	it("reads each child's live state on its own, and counts every ended state in the header", () => {
		// The parent picked one child up again in a running turn; the other one's turns have all ended.
		render(
			<SubagentTeam
				launches={[launch("again", "Audit: checkout"), launch("quiet", "Audit: admin")]}
				steps={new Map()}
				live={new Set(["again"])}
				harness="claude-code"
				/>,
				);
				openTeam();
				expect(within(screen.getByRole("button", { name: /Audit: checkout/ })).getByLabelText("Working")).toBeInTheDocument();
		expect(within(screen.getByRole("button", { name: /Audit: admin/ })).getByText("No report")).toBeInTheDocument();
		const header = screen.getByRole("button", { name: /2 subagents/ });
		expect(within(header).queryByText(/working/)).not.toBeInTheDocument();
		expect(within(header).getByText("1 no report")).toBeInTheDocument();
	});

	it("keeps a disclosure choice in the caller's overrides", async () => {
		const user = userEvent.setup();
		const overrides: Record<string, boolean> = {};
		const pages = launch("pages", "Audit: info pages");
		const element = () => (
			<SubagentTeam
				launches={[pages]}
				steps={new Map()}
				live={new Set(["pages"])}
				harness="claude-code"
				disclosureKey="turn:subagents-1"
				disclosureOverrides={{ ...overrides }}
				onDisclosureChange={(key, open) => {
					overrides[key] = open;
				}}
			/>
		);
		const { rerender } = render(element());
		await user.click(screen.getByRole("button", { name: /1 subagent/ }));
		expect(overrides).toEqual({ "turn:subagents-1": true });
		// The caller owns the choices, so the list opens once it hands them back.
		rerender(element());
		await user.click(screen.getByRole("button", { name: /Audit: info pages/ }));
		expect(overrides).toMatchObject({ "turn:subagents-1:agent:pages": true });
	});

	it("does not call a child that ended before any step Starting", () => {
		const stopped = launch("stopped", "Audit: stopped early", "cancelled");
		const delivered = launch("delivered", "Audit: reported elsewhere");
		delivered.detail = {
			...delivered.detail,
			output: JSON.stringify([{ text: "This agent's report was delivered to you directly.", type: "text" }]),
		} as ConversationActivity["detail"];
		const working = launch("working", "Audit: just launched", "running");
		render(
			<SubagentTeam
				launches={[stopped, delivered, working]}
				steps={new Map()}
				live={new Set(["working"])}
				harness="claude-code"
				/>,
				);
				openTeam();

		expect(within(screen.getByRole("button", { name: /Audit: stopped early/ })).getByText("No steps recorded")).toBeInTheDocument();
		const done = screen.getByRole("button", { name: /Audit: reported elsewhere/ });
		expect(within(done).getByText("No steps recorded")).toBeInTheDocument();
		expect(within(done).queryByText("Starting")).not.toBeInTheDocument();
		// Still at work with nothing recorded yet: that one is starting.
		expect(within(screen.getByRole("button", { name: /Audit: just launched/ })).getByText("Starting")).toBeInTheDocument();
	});
});
