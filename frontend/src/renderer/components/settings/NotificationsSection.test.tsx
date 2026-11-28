import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, renderHook, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { CloudCpError } from "../../lib/cloud-cp/errors";
import { recordCloudInboxSupport, resetCloudInboxSupport } from "../../lib/cloud-notifications";
import { visibleGlobalSettings } from "./settingsCatalog";
import { NotificationsSection } from "./NotificationsSection";
import { useNotificationPreferences } from "../../hooks/useNotificationPreferences";
import type { ReactNode } from "react";

const client = vi.hoisted(() => ({
	getMyNotificationPreferences: vi.fn(),
	setMyNotificationPreferences: vi.fn(),
}));
vi.mock("../../hooks/useCloudCp", () => ({ useCloudCp: () => ({ ready: true, client, baseUrl: "https://cp.test", userId: "u-1" }) }));
// Mentions and comment threads are offered unless a test says they are not.
const offered = vi.hoisted(() => ({ mentions: true }));
vi.mock("../../hooks/useMentionsOffered", () => ({ useMentionsOffered: () => offered.mentions }));

function renderSection() {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(<QueryClientProvider client={queryClient}><NotificationsSection titleHidden={false} /></QueryClientProvider>);
}

// The control plane keeps what a save sent and reads it back, as the real one does.
function serveHeld(initial: Record<string, boolean>) {
	let held = { ...initial };
	client.getMyNotificationPreferences.mockReset().mockImplementation(async () => ({ ...held }));
	client.setMyNotificationPreferences.mockReset().mockImplementation(async (change: Record<string, boolean>) => {
		held = { ...held, ...change };
		return { ...held };
	});
}

beforeEach(() => {
	localStorage.clear();
	offered.mentions = true;
	resetCloudInboxSupport();
	recordCloudInboxSupport("https://cp.test", "supported");
	serveHeld({ mentions: true, teamMentions: true });
});

describe("Settings › Notifications", () => {
	it("has two switches, both on by default, and says the bell always lists mentions", async () => {
		renderSection();
		const me = await screen.findByRole("switch", { name: "Someone mentions me" });
		const team = screen.getByRole("switch", { name: "Someone mentions a team I'm in" });
		expect(me).toBeChecked();
		expect(team).toBeChecked();
		expect(screen.getAllByText("Desktop alert and phone")).toHaveLength(2);
		expect(screen.getByText("Mentions always show in the bell. Turning a switch off only stops the alert.")).toBeInTheDocument();
	});

	it("saves one switch at a time", async () => {
		renderSection();
		await userEvent.click(await screen.findByRole("switch", { name: "Someone mentions a team I'm in" }));
		await waitFor(() => expect(client.setMyNotificationPreferences).toHaveBeenCalledWith({ teamMentions: false }));
		await waitFor(() => expect(screen.getByRole("switch", { name: "Someone mentions a team I'm in" })).not.toBeChecked());
		expect(screen.getByRole("switch", { name: "Someone mentions me" })).toBeChecked();
	});

	it("puts a switch back and says so when saving fails", async () => {
		client.setMyNotificationPreferences.mockRejectedValue(new CloudCpError("Down.", { status: 503 }));
		renderSection();
		await userEvent.click(await screen.findByRole("switch", { name: "Someone mentions me" }));
		expect(await screen.findByText("Could not save notification settings.")).toBeInTheDocument();
		await waitFor(() => expect(screen.getByRole("switch", { name: "Someone mentions me" })).toBeChecked());
	});
});

describe("Settings › Notifications with asides", () => {
	beforeEach(() => serveHeld({ mentions: true, teamMentions: true, asideReplies: true }));

	it("adds a third switch for replies in asides, on by default, under Mentions and comments", async () => {
		renderSection();
		const replies = await screen.findByRole("switch", { name: "New replies to comments I'm in" });
		expect(replies).toBeChecked();
		expect(screen.getAllByRole("switch")).toHaveLength(3);
		expect(screen.getByText("Mentions and comments")).toBeInTheDocument();
		expect(screen.getAllByText("Desktop alert and phone")).toHaveLength(3);
		expect(screen.getByText("These always show in the bell. Turning a switch off only stops the alert.")).toBeInTheDocument();
	});

	it("saves the replies switch on its own", async () => {
		renderSection();
		await userEvent.click(await screen.findByRole("switch", { name: "New replies to comments I'm in" }));
		await waitFor(() => expect(client.setMyNotificationPreferences).toHaveBeenCalledWith({ asideReplies: false }));
		await waitFor(() => expect(screen.getByRole("switch", { name: "New replies to comments I'm in" })).not.toBeChecked());
		expect(screen.getByRole("switch", { name: "Someone mentions me" })).toBeChecked();
	});

	it("leaves the third switch out for a control plane without asides", async () => {
		client.getMyNotificationPreferences.mockReset().mockResolvedValue({ mentions: true, teamMentions: true });
		renderSection();
		await screen.findByRole("switch", { name: "Someone mentions me" });
		expect(screen.getAllByRole("switch")).toHaveLength(2);
		expect(screen.queryByText("Mentions and comments")).toBeNull();
	});
});

describe("the Notifications page in the rail", () => {
	const context = { cloudEnabled: true, diagnostics: false, customCoder: false, selfServeBilling: false };

	it("shows only once the control plane answered with the preferences", () => {
		expect(visibleGlobalSettings({ ...context, notifications: true }).some((item) => item.id === "notifications")).toBe(true);
		expect(visibleGlobalSettings({ ...context, notifications: false }).some((item) => item.id === "notifications")).toBe(false);
		expect(visibleGlobalSettings({ ...context, cloudEnabled: false, notifications: true }).some((item) => item.id === "notifications")).toBe(false);
	});

	it("never asks a control plane without mentions for preferences", async () => {
		resetCloudInboxSupport();
		recordCloudInboxSupport("https://cp.test", "unsupported");
		renderSection();
		await new Promise((resolve) => setTimeout(resolve, 20));
		expect(client.getMyNotificationPreferences).not.toHaveBeenCalled();
		expect(screen.queryByRole("switch")).toBeNull();
	});
});

describe("the Notifications page behind the mentions feature", () => {
	function renderPreferences() {
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
		return renderHook(() => useNotificationPreferences(), { wrapper });
	}

	it("is not listed, and reads nothing, while the control plane does not offer mentions", async () => {
		offered.mentions = false;
		const { result } = renderPreferences();
		await act(async () => undefined);
		expect(result.current.available).toBe(false);
		expect(client.getMyNotificationPreferences).not.toHaveBeenCalled();
	});

	it("is listed once read while it does", async () => {
		const { result } = renderPreferences();
		await waitFor(() => expect(result.current.available).toBe(true));
		expect(client.getMyNotificationPreferences).toHaveBeenCalledTimes(1);
	});
});

describe("saves that overlap", () => {
	it("keeps a later save that went through when an earlier one fails", async () => {
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
		client.getMyNotificationPreferences.mockResolvedValue({ mentions: true, teamMentions: true, asideReplies: true });
		const { result } = renderHook(() => useNotificationPreferences(), { wrapper });
		await waitFor(() => expect(result.current.available).toBe(true));
		let failFirst: (error: unknown) => void = () => undefined;
		client.setMyNotificationPreferences
			.mockImplementationOnce(() => new Promise((_resolve, reject) => { failFirst = reject; }))
			.mockResolvedValueOnce({ mentions: true, teamMentions: false, asideReplies: true });
		// What the server holds once the second save went through and the first failed.
		client.getMyNotificationPreferences.mockResolvedValue({ mentions: true, teamMentions: false, asideReplies: true });
		let first!: Promise<unknown>;
		await act(async () => { first = result.current.save({ mentions: false }).catch(() => undefined); });
		await act(async () => { await result.current.save({ teamMentions: false }); });
		await act(async () => { failFirst(new Error("offline")); await first; });
		await waitFor(() => expect(result.current.preferences).toEqual({ mentions: true, teamMentions: false, asideReplies: true }));
	});
});
