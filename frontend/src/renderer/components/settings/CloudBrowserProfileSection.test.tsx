import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { appI18n } from "../../i18n";
import { CloudBrowserProfileSection } from "./CloudBrowserProfileSection";

const mocks = vi.hoisted(() => ({
	client: { getMyBrowserProfile: vi.fn(), wipeMyBrowserProfile: vi.fn() },
	browserProfiles: true,
	status: "authenticated",
}));

vi.mock("../../lib/cloud-session", () => ({ useCloudSession: () => ({ status: mocks.status }) }));
vi.mock("../../hooks/useCloudCp", () => ({
	useCloudCp: () => ({ client: mocks.client, ready: true, baseUrl: "https://cp.test", userId: "user-1" }),
}));
vi.mock("../../hooks/useCloudOffering", () => ({
	useCloudOffering: () => ({ org: { id: "org-1" }, browserProfiles: mocks.browserProfiles }),
}));

function renderSection() {
	render(
		<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })}>
			<CloudBrowserProfileSection />
		</QueryClientProvider>,
	);
}

describe("CloudBrowserProfileSection", () => {
	beforeEach(() => {
		mocks.browserProfiles = true;
		mocks.status = "authenticated";
		mocks.client.getMyBrowserProfile.mockReset();
		mocks.client.wipeMyBrowserProfile.mockReset();
	});

	it("shows nothing, and asks nothing, on a control plane without browser profiles", () => {
		mocks.browserProfiles = false;
		renderSection();
		expect(screen.queryByText(appI18n.t("settings.cloudBrowserProfile.label"))).toBeNull();
		expect(mocks.client.getMyBrowserProfile).not.toHaveBeenCalled();
	});

	it("shows nothing, and asks nothing, while signed out of Cloud", () => {
		mocks.status = "unauthenticated";
		renderSection();
		expect(screen.queryByText(appI18n.t("settings.cloudBrowserProfile.label"))).toBeNull();
		expect(mocks.client.getMyBrowserProfile).not.toHaveBeenCalled();
	});

	it("wipes saved sign-ins after confirmation and says which sessions keep theirs", async () => {
		mocks.client.getMyBrowserProfile.mockResolvedValue({ saved: true });
		mocks.client.wipeMyBrowserProfile.mockResolvedValue({ wiped: true, runningSessions: 2 });
		renderSection();
		expect(await screen.findByText(appI18n.t("settings.cloudBrowserProfile.saved"))).toBeTruthy();
		expect(mocks.client.getMyBrowserProfile).toHaveBeenCalledWith("org-1", expect.anything());

		fireEvent.click(screen.getByRole("button", { name: appI18n.t("settings.cloudBrowserProfile.wipe") }));
		const dialog = await screen.findByRole("dialog");
		expect(within(dialog).getByText(appI18n.t("settings.cloudBrowserProfile.confirmTitle"))).toBeTruthy();
		expect(mocks.client.wipeMyBrowserProfile).not.toHaveBeenCalled();

		fireEvent.click(within(dialog).getByRole("button", { name: appI18n.t("settings.cloudBrowserProfile.wipe") }));
		await waitFor(() => expect(mocks.client.wipeMyBrowserProfile).toHaveBeenCalledWith("org-1"));
		expect(await screen.findByText(appI18n.t("settings.cloudBrowserProfile.runningSessions", { count: 2 }))).toBeTruthy();
		await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
	});

	it("cannot wipe when nothing is saved", async () => {
		mocks.client.getMyBrowserProfile.mockResolvedValue({ saved: false });
		renderSection();
		expect(await screen.findByText(appI18n.t("settings.cloudBrowserProfile.none"))).toBeTruthy();
		expect(screen.getByRole("button", { name: appI18n.t("settings.cloudBrowserProfile.wipe") }).hasAttribute("disabled")).toBe(true);
	});

	it("keeps the dialog open with the failure when the wipe fails", async () => {
		mocks.client.getMyBrowserProfile.mockResolvedValue({ saved: true });
		mocks.client.wipeMyBrowserProfile.mockRejectedValue(new Error("network"));
		renderSection();
		// Wipe is disabled until the saved sign-ins have loaded.
		expect(await screen.findByText(appI18n.t("settings.cloudBrowserProfile.saved"))).toBeTruthy();
		fireEvent.click(screen.getByRole("button", { name: appI18n.t("settings.cloudBrowserProfile.wipe") }));
		const dialog = await screen.findByRole("dialog");
		fireEvent.click(within(dialog).getByRole("button", { name: appI18n.t("settings.cloudBrowserProfile.wipe") }));
		expect(await within(dialog).findByText(appI18n.t("settings.cloudBrowserProfile.wipeFailed"))).toBeTruthy();
	});
});
