import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

vi.mock("../lib/api-client", () => ({
	getApiBaseUrl: () => "http://127.0.0.1:3001",
}));

import { ComputerUsePanel } from "./ComputerUsePanel";
import type { ComputerUseTimelineEntry } from "../hooks/useComputerUseTimeline";

const shot = (name: string) => `http://127.0.0.1:3001/api/v1/computer-use/sessions/ao-1/screenshots/${name}`;

const entries: ComputerUseTimelineEntry[] = [
	{ id: "1", at: "2026-11-16T09:00:00Z", tool: "launch_app", arguments: { name: "Safari" }, ok: true, screenshots: ["one.png"] },
	{ id: "2", at: "2026-11-16T09:00:05Z", tool: "type_text", arguments: { text: "hello" }, ok: true, screenshots: ["two.png"] },
	{
		id: "3",
		at: "2026-11-16T09:00:09Z",
		tool: "click",
		arguments: {},
		ok: false,
		error: "window_id_not_found: window 9 no longer exists (COMPUTER_USE_CALL_FAILED)",
		screenshots: [],
	},
];

describe("ComputerUsePanel", () => {
	it("names the other computer the latest step ran on", () => {
		render(<ComputerUsePanel entries={[...entries, { id: "4", at: "2026-11-16T09:01:00Z", tool: "list_windows", ok: true, ranOn: "Studio Mac", screenshots: [] }]} sessionId="ao-1" />);
		expect(screen.getByText("Studio Mac")).toBeInTheDocument();
		expect(screen.queryByText("This machine")).not.toBeInTheDocument();
	});

	it("shows the latest screenshot and every step, newest first", () => {
		render(<ComputerUsePanel entries={entries} sessionId="ao-1" />);

		const hero = screen.getByRole("img", { name: 'Typed "hello"' });
		expect(hero).toHaveAttribute("src", shot("two.png"));
		expect(screen.getByText("Runs on")).toBeInTheDocument();
		expect(screen.getByText("This machine")).toBeInTheDocument();

		const steps = within(screen.getByRole("list", { name: "Timeline" })).getAllByRole("listitem");
		expect(steps.map((step) => step.textContent)).toEqual([
			expect.stringContaining("Couldn't click"),
			expect.stringContaining('Typed "hello"'),
			expect.stringContaining("Opened Safari"),
		]);
		// A failed step says why, without the code meant for the agent, and has no screenshot to open.
		expect(within(steps[0]).getByText("window_id_not_found: window 9 no longer exists")).toBeInTheDocument();
		expect(within(steps[0]).queryByRole("button")).not.toBeInTheDocument();
	});

	it("opens a step's screenshot at full size", async () => {
		render(<ComputerUsePanel entries={entries} sessionId="ao-1" />);

		await userEvent.click(screen.getByRole("button", { name: "Open screenshot: Opened Safari" }));

		const dialog = await screen.findByRole("dialog");
		expect(within(dialog).getByRole("img", { name: "Opened Safari" })).toHaveAttribute("src", shot("one.png"));
	});

	it("holds the screenshot's place when no step has one", () => {
		render(<ComputerUsePanel entries={[entries[2]]} sessionId="ao-1" />);
		expect(screen.getByText("No screenshot yet")).toBeInTheDocument();
	});
});
