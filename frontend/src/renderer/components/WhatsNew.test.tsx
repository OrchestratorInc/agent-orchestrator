import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { WhatsNewEntry } from "../../shared/whats-new";

const mocks = vi.hoisted(() => ({
	items: [] as WhatsNewEntry[],
	dismiss: vi.fn(async (_ids: string[]) => undefined),
	openExternal: vi.fn(async (_url: string) => undefined),
	capture: vi.fn(async (_event: string, _props?: unknown) => true),
	openGlobalSettings: vi.fn(),
}));

vi.mock("../lib/bridge", () => ({
	aoBridge: {
		whatsNew: { list: async () => mocks.items, dismiss: mocks.dismiss, onChanged: () => () => undefined },
		app: { openExternal: mocks.openExternal },
	},
}));
vi.mock("../lib/telemetry", () => ({ captureRendererEvent: mocks.capture }));
vi.mock("../stores/ui-store", () => ({
	useUiStore: (selector: (state: { openGlobalSettings: typeof mocks.openGlobalSettings }) => unknown) =>
		selector({ openGlobalSettings: mocks.openGlobalSettings }),
}));

import { setWhatsNewPanelOpen } from "../lib/whats-new";
import { WhatsNewCard, WhatsNewDialog, formatPublishedAt } from "./WhatsNew";

const announcement: WhatsNewEntry = {
	id: "2026-10-notifications",
	kind: "announcement",
	title: "Know when agents finish",
	body: "Orchestrator now notifies you when an agent finishes.",
	publishedAt: "2026-10-28",
	cta: { label: "Read more", url: "https://docs.orchestrator.inc/docs/" },
	seen: false,
};
const nudge: WhatsNewEntry = {
	id: "update-0-15-2",
	kind: "nudge",
	title: "Update to fix terminal input",
	body: "Versions before 0.15.2 can drop keystrokes.",
	publishedAt: "2026-10-27",
	maxVersion: "0.15.2",
	cta: { label: "Update", action: "update" },
	seen: false,
};

function renderWithClient() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<WhatsNewCard tabIndex={0} />
			<WhatsNewDialog />
		</QueryClientProvider>,
	);
}

describe("WhatsNewCard", () => {
	beforeEach(() => {
		mocks.items = [announcement, nudge];
		mocks.dismiss.mockClear();
		mocks.openExternal.mockClear();
		mocks.capture.mockClear();
		mocks.openGlobalSettings.mockClear();
		act(() => setWhatsNewPanelOpen(false));
	});

	it("shows the newest unseen item and reports one impression", async () => {
		renderWithClient();
		expect(await screen.findByText("Know when agents finish")).toBeInTheDocument();
		expect(screen.queryByText("Update to fix terminal input")).not.toBeInTheDocument();
		await waitFor(() => expect(mocks.capture).toHaveBeenCalledWith("ao.whats_new.shown", { item_id: "2026-10-notifications" }));
	});

	it("opens the link, reports the click and marks the item seen", async () => {
		renderWithClient();
		fireEvent.click(await screen.findByRole("button", { name: "Read more" }));
		expect(mocks.openExternal).toHaveBeenCalledWith("https://docs.orchestrator.inc/docs/");
		expect(mocks.capture).toHaveBeenCalledWith("ao.whats_new.clicked", { item_id: "2026-10-notifications" });
		expect(mocks.dismiss).toHaveBeenCalledWith(["2026-10-notifications"]);
		expect(await screen.findByText("Update to fix terminal input")).toBeInTheDocument();
	});

	it("sends a nudge's update action to update settings", async () => {
		mocks.items = [nudge];
		renderWithClient();
		fireEvent.click(await screen.findByRole("button", { name: "Update" }));
		expect(mocks.openGlobalSettings).toHaveBeenCalledWith("updates");
	});

	it("dismisses every unseen item at once and reports the dismissal", async () => {
		renderWithClient();
		fireEvent.click(await screen.findByRole("button", { name: "Dismiss" }));
		expect(mocks.capture).toHaveBeenCalledWith("ao.whats_new.dismissed", { item_id: "2026-10-notifications" });
		expect(mocks.dismiss).toHaveBeenCalledWith(["2026-10-notifications", "update-0-15-2"]);
		await waitFor(() => expect(screen.queryByTestId("whats-new-card")).not.toBeInTheDocument());
	});

	it("reopens every item in the panel, including seen ones", async () => {
		mocks.items = [{ ...announcement, seen: true }, nudge];
		renderWithClient();
		expect(await screen.findByText("Update to fix terminal input")).toBeInTheDocument();
		act(() => setWhatsNewPanelOpen(true));
		const panel = await screen.findByTestId("whats-new-panel");
		expect(panel).toHaveTextContent("Know when agents finish");
		expect(panel).toHaveTextContent("Update to fix terminal input");
		await waitFor(() => expect(mocks.dismiss).toHaveBeenCalledWith(["update-0-15-2"]));
	});

	it("renders nothing without unseen items", async () => {
		mocks.items = [{ ...announcement, seen: true }];
		renderWithClient();
		await waitFor(() => expect(screen.queryByTestId("whats-new-card")).not.toBeInTheDocument());
	});
});

describe("formatPublishedAt", () => {
	it("keeps a date-only publish date on its calendar day", () => {
		expect(formatPublishedAt("2026-11-03")).toBe(new Intl.DateTimeFormat(undefined, { month: "short", day: "numeric" }).format(new Date(2026, 10, 3)));
	});
});
