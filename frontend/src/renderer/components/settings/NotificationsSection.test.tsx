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
	it("lists mentions under Needs you, with a desktop switch that is on by default and can be turned off", async () => {
		preferences = [
			...preferences,
			{ kind: "mentioned", inApp: true, desktop: true, scope: "following", locked: false, tier: 0 },
		];
		renderSection();
		await waitFor(() => expect(rowOf("mentioned")).toBeTruthy());
		// Between the Needs you heading and the next group's.
		const after = (a: Node, b: Node) => Boolean(a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING);
		expect(after(screen.getByRole("heading", { level: 2, name: "Needs you" }), rowOf("mentioned"))).toBe(true);
		expect(after(rowOf("mentioned"), screen.getByRole("heading", { level: 2, name: "Agents" }))).toBe(true);
		expect(within(rowOf("mentioned")).getByText("Mentioned")).toBeInTheDocument();
		const toggle = within(rowOf("mentioned")).getByRole("switch", { name: "Mentioned on the desktop" });
		expect(toggle).toBeChecked();
		expect(toggle).toBeEnabled();
		await userEvent.click(toggle);
		await waitFor(() => expect(setNotificationPreferences).toHaveBeenCalledWith([expect.objectContaining({ kind: "mentioned", desktop: false, inApp: true })]));
	});

	it("groups each kind under Needs you, Agents and Pull requests with one desktop switch", async () => {
		renderSection();
		await waitFor(() => expect(rowOf("approval_needed")).toBeTruthy());

		// The first heading is the page's own; on its page in Settings it is hidden as a repeat of the title.
		expect(screen.getAllByRole("heading", { level: 2 }).map((heading) => heading.textContent)).toEqual([
			"Notifications", "Needs you", "Agents", "Pull requests",
		]);
		expect(screen.getByText("Everything shows in the bell. Choose what also alerts your desktop.")).toBeInTheDocument();
		expect(within(rowOf("approval_needed")).getByText("Approval needed")).toBeInTheDocument();
		expect(within(rowOf("turn_finished")).getByRole("switch", { name: "Turn finished on the desktop" })).not.toBeChecked();
		expect(within(rowOf("approval_needed")).getByRole("switch", { name: "Approval needed on the desktop" })).toBeChecked();
		// No other control per row: no in-app box, no per-kind sessions menu.
		expect(document.querySelectorAll("[data-notification-kind] [role=checkbox]")).toHaveLength(0);
		expect(within(rowOf("agent_failed")).queryByRole("button")).not.toBeInTheDocument();
	});

	it("saves a kind's desktop switch, keeping it in the app", async () => {
		renderSection();
		await waitFor(() => expect(rowOf("agent_failed")).toBeTruthy());

		await userEvent.click(within(rowOf("agent_failed")).getByRole("switch", { name: "Agent failed on the desktop" }));
		expect(setNotificationPreferences).toHaveBeenLastCalledWith([{ kind: "agent_failed", inApp: true, desktop: false, scope: "following" }]);
		await waitFor(() => expect(within(rowOf("agent_failed")).getByRole("switch", { name: "Agent failed on the desktop" })).not.toBeChecked());
	});

	it("sets whose sessions notify the person, for every kind at once", async () => {
		renderSection();
		await waitFor(() => expect(rowOf("agent_failed")).toBeTruthy());

		await userEvent.click(screen.getByRole("button", { name: "Notify me about" }));
		await userEvent.click(await screen.findByRole("menuitem", { name: "Only my sessions" }));
		const saved = setNotificationPreferences.mock.lastCall?.[0] as CloudCpNotificationPreferenceInput[];
		expect(saved).toHaveLength(preferences.length);
		expect(saved.every((item) => item.scope === "mine" && item.inApp)).toBe(true);
		await waitFor(() => expect(screen.getByRole("button", { name: "Notify me about" })).toHaveTextContent("Only my sessions"));
	});

	it("says so when a change can't be saved, and puts the choice back", async () => {
		setNotificationPreferences.mockRejectedValue(new CloudCpError("Service unavailable.", { status: 503 }));
		renderSection();
		await waitFor(() => expect(rowOf("pr_merged")).toBeTruthy());

		await userEvent.click(within(rowOf("pr_merged")).getByRole("switch", { name: "PR merged on the desktop" }));

		expect(await screen.findByText("Could not save notification settings.")).toBeInTheDocument();
		expect(within(rowOf("pr_merged")).getByRole("switch", { name: "PR merged on the desktop" })).not.toBeChecked();
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
