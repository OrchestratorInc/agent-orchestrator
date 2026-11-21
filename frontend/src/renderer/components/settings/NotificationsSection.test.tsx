import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { CloudCpError } from "../../lib/cloud-cp/errors";
import { recordCloudInboxSupport, resetCloudInboxSupport } from "../../lib/cloud-notifications";
import { visibleGlobalSettings } from "./settingsCatalog";
import { NotificationsSection } from "./NotificationsSection";

const client = vi.hoisted(() => ({
	getMyNotificationPreferences: vi.fn(),
	setMyNotificationPreferences: vi.fn(),
}));
vi.mock("../../hooks/useCloudCp", () => ({ useCloudCp: () => ({ ready: true, client, baseUrl: "https://cp.test", userId: "u-1" }) }));

function renderSection() {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(<QueryClientProvider client={queryClient}><NotificationsSection titleHidden={false} /></QueryClientProvider>);
}

beforeEach(() => {
	localStorage.clear();
	resetCloudInboxSupport();
	recordCloudInboxSupport("https://cp.test", "supported");
	client.getMyNotificationPreferences.mockReset().mockResolvedValue({ mentions: true, teamMentions: true });
	client.setMyNotificationPreferences.mockReset().mockImplementation(async (change) => ({ mentions: true, teamMentions: true, ...change }));
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
	beforeEach(() => {
		client.getMyNotificationPreferences.mockReset().mockResolvedValue({ mentions: true, teamMentions: true, asideReplies: true });
		client.setMyNotificationPreferences.mockReset().mockImplementation(async (change) => ({ mentions: true, teamMentions: true, asideReplies: true, ...change }));
	});

	it("adds a third switch for replies in asides, on by default, under Mentions and asides", async () => {
		renderSection();
		const replies = await screen.findByRole("switch", { name: "New replies in asides I'm in" });
		expect(replies).toBeChecked();
		expect(screen.getAllByRole("switch")).toHaveLength(3);
		expect(screen.getByText("Mentions and asides")).toBeInTheDocument();
		expect(screen.getAllByText("Desktop alert and phone")).toHaveLength(3);
		expect(screen.getByText("These always show in the bell. Turning a switch off only stops the alert.")).toBeInTheDocument();
	});

	it("saves the replies switch on its own", async () => {
		renderSection();
		await userEvent.click(await screen.findByRole("switch", { name: "New replies in asides I'm in" }));
		await waitFor(() => expect(client.setMyNotificationPreferences).toHaveBeenCalledWith({ asideReplies: false }));
		await waitFor(() => expect(screen.getByRole("switch", { name: "New replies in asides I'm in" })).not.toBeChecked());
		expect(screen.getByRole("switch", { name: "Someone mentions me" })).toBeChecked();
	});

	it("leaves the third switch out for a control plane without asides", async () => {
		client.getMyNotificationPreferences.mockReset().mockResolvedValue({ mentions: true, teamMentions: true });
		renderSection();
		await screen.findByRole("switch", { name: "Someone mentions me" });
		expect(screen.getAllByRole("switch")).toHaveLength(2);
		expect(screen.queryByText("Mentions and asides")).toBeNull();
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
