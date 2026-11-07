import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { CloudCpError } from "../../lib/cloud-cp/errors";
import type { CloudCpNotificationPreference, CloudCpNotificationPreferenceInput } from "../../lib/cloud-cp/types";
import { cloudInboxSupport, resetCloudInboxSupport } from "../../lib/cloud-notifications";
import { useSoundNotificationsStore } from "../../stores/sound-notifications-store";
import { TooltipProvider } from "../ui/tooltip";
import { NotificationsSection } from "./NotificationsSection";
import { visibleGlobalSettings } from "./settingsCatalog";

const { getNotificationPreferences, setNotificationPreferences } = vi.hoisted(() => ({
	getNotificationPreferences: vi.fn(),
	setNotificationPreferences: vi.fn(),
}));

vi.mock("../../hooks/useCloudCp", () => ({
	useCloudCp: () => ({
		client: { getNotificationPreferences, setNotificationPreferences },
		ready: true,
		baseUrl: "https://cloud.test",
		userId: "me",
	}),
}));

let preferences: CloudCpNotificationPreference[];

function renderSection() {
	return render(
		<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
			<TooltipProvider>
				<NotificationsSection titleHidden={false} />
			</TooltipProvider>
		</QueryClientProvider>,
	);
}

const rowOf = (kind: string) => document.querySelector<HTMLElement>(`[data-notification-kind="${kind}"]`)!;

beforeEach(() => {
	resetCloudInboxSupport();
	preferences = [
		{ kind: "approval_needed", inApp: true, desktop: true, scope: "following", locked: true, tier: 0 },
		{ kind: "agent_failed", inApp: true, desktop: true, scope: "following", locked: false, tier: 1 },
		{ kind: "turn_finished", inApp: true, desktop: false, scope: "mine", locked: false, tier: 2 },
		{ kind: "pr_merged", inApp: true, desktop: false, scope: "following", locked: false, tier: 2 },
	];
	getNotificationPreferences.mockReset().mockImplementation(async () => ({ items: preferences }));
	setNotificationPreferences.mockReset().mockImplementation(async (items: CloudCpNotificationPreferenceInput[]) => {
		preferences = preferences.map((preference) => {
			const change = items.find((item) => item.kind === preference.kind);
			return change ? { ...preference, ...change, scope: change.scope ?? preference.scope, inApp: preference.locked || change.inApp } : preference;
		});
		return { items: preferences };
	});
	useSoundNotificationsStore.setState({ enabled: true, loaded: true, saving: false, saveError: false });
});

afterEach(() => {
	resetCloudInboxSupport();
});

describe("NotificationsSection", () => {
	it("groups each kind under Needs you, Agents and Pull requests with its channels", async () => {
		renderSection();
		await waitFor(() => expect(rowOf("approval_needed")).toBeTruthy());

		// The first heading is the page's own; on its page in Settings it is hidden as a repeat of the title.
		expect(screen.getAllByRole("heading", { level: 2 }).map((heading) => heading.textContent)).toEqual([
			"Notifications", "Needs you", "Agents", "Pull requests",
		]);
		expect(within(rowOf("approval_needed")).getByText("Approval needed")).toBeInTheDocument();
		expect(within(rowOf("turn_finished")).getByRole("checkbox", { name: "Turn finished on the desktop" })).not.toBeChecked();
		expect(within(rowOf("pr_merged")).getByRole("checkbox", { name: "PR merged in app" })).toBeChecked();
	});

	it("keeps a blocking kind on in the app, shown with a lock", async () => {
		renderSection();
		await waitFor(() => expect(rowOf("approval_needed")).toBeTruthy());
		const row = within(rowOf("approval_needed"));

		expect(row.getByRole("img", { name: "Always on in the app, so an agent never waits unseen" })).toBeInTheDocument();
		expect(row.getByRole("checkbox", { name: "Approval needed in app" })).toBeChecked();
		expect(row.getByRole("checkbox", { name: "Approval needed in app" })).toBeDisabled();
		expect(row.getByRole("checkbox", { name: "Approval needed on the desktop" })).toBeEnabled();
		expect(within(rowOf("agent_failed")).queryByRole("img")).not.toBeInTheDocument();
	});

	it("saves one kind's channel and scope as they change", async () => {
		renderSection();
		await waitFor(() => expect(rowOf("agent_failed")).toBeTruthy());

		await userEvent.click(within(rowOf("agent_failed")).getByRole("checkbox", { name: "Agent failed on the desktop" }));
		expect(setNotificationPreferences).toHaveBeenLastCalledWith([{ kind: "agent_failed", inApp: true, desktop: false, scope: "following" }]);
		await waitFor(() => expect(within(rowOf("agent_failed")).getByRole("checkbox", { name: "Agent failed on the desktop" })).not.toBeChecked());

		await userEvent.click(within(rowOf("agent_failed")).getByRole("button", { name: "Sessions for Agent failed" }));
		await userEvent.click(await screen.findByRole("menuitem", { name: "Mine" }));
		expect(setNotificationPreferences).toHaveBeenLastCalledWith([{ kind: "agent_failed", inApp: true, desktop: false, scope: "mine" }]);
	});

	it("says so when a change can't be saved, and puts the choice back", async () => {
		setNotificationPreferences.mockRejectedValue(new CloudCpError("Service unavailable.", { status: 503 }));
		renderSection();
		await waitFor(() => expect(rowOf("pr_merged")).toBeTruthy());

		await userEvent.click(within(rowOf("pr_merged")).getByRole("checkbox", { name: "PR merged on the desktop" }));

		expect(await screen.findByText("Could not save notification settings.")).toBeInTheDocument();
		expect(within(rowOf("pr_merged")).getByRole("checkbox", { name: "PR merged on the desktop" })).not.toBeChecked();
	});

	it("shares the sound switch with General", async () => {
		const setEnabled = vi.fn(async () => undefined);
		useSoundNotificationsStore.setState({ setEnabled });
		renderSection();
		await userEvent.click(screen.getByRole("switch", { name: "Sound notifications" }));
		expect(setEnabled).toHaveBeenCalledWith(false);
	});

	it("learns that a control plane without preferences has no team notifications", async () => {
		getNotificationPreferences.mockRejectedValue(new CloudCpError("404 page not found", { status: 404 }));
		renderSection();
		await waitFor(() => expect(cloudInboxSupport("https://cloud.test")).toBe("unsupported"));
		expect(document.querySelector("[data-notification-kind]")).toBeNull();
	});
});

describe("Settings › Notifications in the rail", () => {
	const context = { cloudEnabled: true, diagnostics: false, customCoder: false, selfServeBilling: false };

	it("is listed only when signed in to a control plane that serves preferences", () => {
		const ids = (notifications: boolean, cloudEnabled = true) =>
			visibleGlobalSettings({ ...context, cloudEnabled, notifications }).map((item) => item.id);
		expect(ids(true)).toContain("notifications");
		expect(ids(false)).not.toContain("notifications");
		expect(ids(true, false)).not.toContain("notifications");
	});
});
