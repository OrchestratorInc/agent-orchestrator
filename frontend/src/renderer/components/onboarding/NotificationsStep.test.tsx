import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
	test: vi.fn(async (_copy: { title: string; body: string }) => "granted" as string),
	openExternal: vi.fn(async (_url: string) => undefined),
	capture: vi.fn(async (_event: string, _props?: unknown) => true),
	steps: [] as Array<[string, string]>,
	mac: true,
}));

vi.mock("../../lib/bridge", () => ({
	aoBridge: { notifications: { test: mocks.test }, app: { openExternal: mocks.openExternal } },
}));
vi.mock("../../lib/telemetry", () => ({ captureRendererEvent: mocks.capture }));
vi.mock("../../lib/platform", () => ({ isMacPlatform: () => mocks.mac, isWindowsPlatform: () => false }));
vi.mock("../../lib/onboarding-telemetry", () => ({
	reportOnboardingStep: (step: string, phase: string) => mocks.steps.push([step, phase]),
	useOnboardingStep: (step: string, active: boolean) => {
		if (active) mocks.steps.push([step, "active"]);
		return () => mocks.steps.push([step, "completed"]);
	},
}));

import { NOTIFICATIONS_STEP_STORAGE_KEY, NotificationsStep } from "./NotificationsStep";

describe("NotificationsStep", () => {
	beforeEach(() => {
		window.localStorage.clear();
		mocks.test.mockReset();
		mocks.test.mockResolvedValue("granted");
		mocks.openExternal.mockClear();
		mocks.capture.mockClear();
		mocks.steps = [];
	});

	it("explains what it notifies about during onboarding and records the offer", async () => {
		render(<NotificationsStep onboarding />);
		expect(screen.getByText("Get notified when agents need you")).toBeInTheDocument();
		expect(screen.getByText(/finishes, opens a pull request, or needs your input/)).toBeInTheDocument();
		expect(mocks.steps).toContainEqual(["notifications", "active"]);
		await waitFor(() => expect(window.localStorage.getItem(NOTIFICATIONS_STEP_STORAGE_KEY)).toBe("offered"));
	});

	it("stays hidden for installs that never saw onboarding, and returns for ones that did", () => {
		const { unmount } = render(<NotificationsStep onboarding={false} />);
		expect(screen.queryByTestId("notifications-step")).not.toBeInTheDocument();
		unmount();
		window.localStorage.setItem(NOTIFICATIONS_STEP_STORAGE_KEY, "offered");
		render(<NotificationsStep onboarding={false} />);
		expect(screen.getByTestId("notifications-step")).toBeInTheDocument();
	});

	it("sends a localized test notification, reports the permission and completes the step", async () => {
		render(<NotificationsStep onboarding />);
		fireEvent.click(screen.getByRole("button", { name: "Turn on notifications" }));
		expect(await screen.findByText(/Notifications are on. You should see/)).toBeInTheDocument();
		expect(mocks.test).toHaveBeenCalledWith({ title: "Notifications are on", body: expect.stringContaining("need your input") });
		expect(mocks.capture).toHaveBeenCalledWith("ao.notifications.permission", { state: "granted" });
		expect(mocks.steps).toContainEqual(["notifications", "completed"]);
		expect(window.localStorage.getItem(NOTIFICATIONS_STEP_STORAGE_KEY)).toBe("done");
		fireEvent.click(screen.getByRole("button", { name: "Done" }));
		expect(screen.queryByTestId("notifications-step")).not.toBeInTheDocument();
	});

	it("diagnoses blocked macOS notifications and links to System Settings", async () => {
		mocks.test.mockResolvedValue("denied");
		render(<NotificationsStep onboarding />);
		fireEvent.click(screen.getByRole("button", { name: "Turn on notifications" }));
		expect(await screen.findByText(/macOS is blocking notifications/)).toBeInTheDocument();
		expect(mocks.capture).toHaveBeenCalledWith("ao.notifications.permission", { state: "denied" });
		fireEvent.click(screen.getByRole("button", { name: "Open System Settings" }));
		expect(mocks.openExternal).toHaveBeenCalledWith(
			"x-apple.systempreferences:com.apple.Notifications-Settings.extension?id=dev.agent-orchestrator.desktop",
		);
		expect(mocks.steps).not.toContainEqual(["notifications", "completed"]);
	});

	it("records a skip and does not come back", () => {
		const { unmount } = render(<NotificationsStep onboarding />);
		fireEvent.click(screen.getByRole("button", { name: "Skip" }));
		expect(mocks.steps).toContainEqual(["notifications", "skipped"]);
		expect(screen.queryByTestId("notifications-step")).not.toBeInTheDocument();
		unmount();
		render(<NotificationsStep onboarding />);
		expect(screen.queryByTestId("notifications-step")).not.toBeInTheDocument();
	});
});
