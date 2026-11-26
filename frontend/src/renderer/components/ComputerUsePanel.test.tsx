import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render as renderUi, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactElement } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const apiMocks = vi.hoisted(() => ({
	target: undefined as { hostId: string; label: string; url: string } | undefined,
	sessionTarget: undefined as string | undefined,
	GET: vi.fn(),
	DELETE: vi.fn(),
}));

vi.mock("../lib/api-client", () => ({
	getApiBaseUrl: () => "http://127.0.0.1:3001",
	apiClient: { GET: apiMocks.GET, DELETE: apiMocks.DELETE },
}));

import { ComputerUsePanel } from "./ComputerUsePanel";

function render(ui: ReactElement) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return renderUi(<QueryClientProvider client={client}>{ui}</QueryClientProvider>);
}

beforeEach(() => {
	apiMocks.target = undefined;
	apiMocks.sessionTarget = undefined;
	apiMocks.GET.mockReset().mockImplementation(async (path: string) => path.endsWith("/target")
		? { data: { target: apiMocks.sessionTarget } }
		: { data: { allowRemote: false, target: apiMocks.target, activity: [] } });
	apiMocks.DELETE.mockReset().mockImplementation(async () => ({ data: { allowRemote: false, activity: [] } }));
});
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
	it("names the computer computer use runs on", async () => {
		apiMocks.target = { hostId: "h_mac", label: "Studio Mac", url: "http://192.0.2.1:1" };
		render(<ComputerUsePanel entries={entries} sessionId="ao-1" />);
		const picker = await screen.findByRole("button", { name: "Runs on" });
		await waitFor(() => expect(picker).toHaveTextContent("Studio Mac"));
	});

	it("names the computer this chat runs on, not the one Settings chose since", async () => {
		apiMocks.target = { hostId: "h_mac", label: "Studio Mac", url: "http://192.0.2.1:1" };
		apiMocks.sessionTarget = "local";
		render(<ComputerUsePanel entries={entries} sessionId="ao-1" />);
		const picker = await screen.findByRole("button", { name: "Runs on" });
		await waitFor(() => expect(picker).toHaveTextContent("This computer"));
		expect(apiMocks.GET).toHaveBeenCalledWith("/api/v1/computer-use/sessions/{sessionId}/target", expect.objectContaining({ params: { path: { sessionId: "ao-1" } } }));
	});

	it("moves this chat's computer use back to this computer from the panel", async () => {
		render(<ComputerUsePanel entries={entries} sessionId="ao-1" />);
		const picker = await screen.findByRole("button", { name: "Runs on" });
		await waitFor(() => expect(picker).toBeEnabled());
		await userEvent.click(picker);
		await userEvent.click(await screen.findByRole("menuitem", { name: /This computer/ }));
		// Choosing the current computer still moves this chat onto it.
		await waitFor(() => expect(apiMocks.DELETE).toHaveBeenCalledWith("/api/v1/computer-use/remote/target", { params: { query: { sessionId: "ao-1" } } }));
	});

	it("shows the latest screenshot and every step, newest first", () => {
		render(<ComputerUsePanel entries={entries} sessionId="ao-1" />);

		const hero = screen.getByRole("img", { name: 'Typed "hello"' });
		expect(hero).toHaveAttribute("src", shot("two.png"));
		expect(screen.getByText("Runs on")).toBeInTheDocument();

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
