import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({
	navState: null as { key: string } | null,
	navigate: vi.fn(),
	launch: null as unknown,
	org: null as { id: string } | null,
	hostStates: [] as Array<{ hostId: string; connection: string; config: Record<string, unknown> | null }>,
}));
vi.mock("expo-device", () => ({ deviceName: "Phone" }));
vi.mock("expo-notifications", () => ({ addNotificationResponseReceivedListener: () => ({ remove() {} }) }));
vi.mock("expo-router", () => ({ useRouter: () => ({ navigate: fixtures.navigate }), useRootNavigationState: () => fixtures.navState }));
vi.mock("./pageNavigation", () => ({ useOpenPage: () => vi.fn() }));
vi.mock("./store", () => ({ useApp: () => ({ hostStates: fixtures.hostStates }) }));
vi.mock("./cloud/CloudProvider", () => ({ useCloud: () => ({ client: {}, org: fixtures.org, me: null }) }));
vi.mock("./telemetry/runtime", () => ({ mobileTelemetry: () => null }));
vi.mock("./api", () => ({ markNotificationRead: vi.fn(async () => {}) }));
vi.mock("./installId", () => ({ getInstallId: async () => "install" }));
vi.mock("./hosts", () => ({ findHost: async (id: string) => (id === "h_a" ? { id: "h_a" } : null) }));
vi.mock("./launchNotification", () => ({
	launchNotificationResponse: async () => fixtures.launch,
	markNotificationResponseHandled: async () => {},
}));
vi.mock("./push", () => ({
	configurePushHandler: () => {}, ensureAndroidChannel: async () => {}, ensureApprovalCategory: async () => {},
	announcePairedDevice: async () => {}, registerForPush: async () => {}, onManualPushRegistration: () => () => {},
	retryPendingUnpairs: async () => {},
}));
vi.mock("./approvals/localApprovals", () => ({ decideApproval: vi.fn(async () => "decided") }));
vi.mock("./approvals/cloudApprovals", () => ({ decideCloudApproval: vi.fn(async () => "decided") }));
vi.mock("react-native", () => ({ Platform: { OS: "ios" }, Alert: { alert: vi.fn() }, AppState: { currentState: "active", addEventListener: () => ({ remove() {} }) } }));

import { decideCloudApproval } from "./approvals/cloudApprovals";
import { decideApproval } from "./approvals/localApprovals";
import { PushManager } from "./PushManager";

const data = { type: "browser_approval", hostId: "h_a", sessionId: "s1", approvalId: "apr_1", notificationId: "n1" };
const response = (actionIdentifier: string) => ({ actionIdentifier, notification: { request: { content: { data } } } });
const config = { hostId: "h_a", host: "desktop.local", httpPort: "3011", password: "t" };

let renderer: ReactTestRenderer | undefined;
beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixtures.navState = null;
	fixtures.hostStates = [];
	fixtures.org = null;
	fixtures.navigate.mockClear();
	vi.mocked(decideCloudApproval).mockClear();
	vi.mocked(decideApproval).mockClear();
});
afterEach(async () => {
	if (renderer) await act(async () => renderer?.unmount());
	renderer = undefined;
	vi.unstubAllGlobals();
});

async function launch(actionIdentifier: string) {
	fixtures.launch = response(actionIdentifier);
	await act(async () => { renderer = create(<PushManager />); });
	// Navigation becomes ready after the launch, as on a cold start.
	fixtures.navState = { key: "root" };
	await act(async () => { renderer!.update(<PushManager />); await Promise.resolve(); });
	await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
}

describe("approval notification on a cold start", () => {
	it("a tap that launched the app opens the session's preview with the approval once navigation is ready", async () => {
		await launch("expo.modules.notifications.actions.DEFAULT");
		expect(fixtures.navigate).toHaveBeenCalledWith("/preview/s1?hostId=h_a&approval=apr_1");
	});

	it("Deny that launched the app is sent once the machine connects, not dropped", async () => {
		await launch("deny");
		// No machine is connected yet on a cold start.
		expect(decideApproval).not.toHaveBeenCalled();
		expect(fixtures.navigate).not.toHaveBeenCalled();
		fixtures.hostStates = [{ hostId: "h_a", connection: "open", config }];
		await act(async () => { renderer!.update(<PushManager />); await Promise.resolve(); });
		await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
		expect(decideApproval).toHaveBeenCalledExactlyOnceWith(config, "apr_1", "deny");
	});

	it("a Cloud Deny that launched the app is sent once the organization loads", async () => {
		fixtures.launch = { actionIdentifier: "deny", notification: { request: { content: { data: { ...data, source: "cloud", orgId: "o1" } } } } };
		await act(async () => { renderer = create(<PushManager />); });
		fixtures.navState = { key: "root" };
		await act(async () => { renderer!.update(<PushManager />); await new Promise((resolve) => setTimeout(resolve, 0)); });
		expect(decideCloudApproval).not.toHaveBeenCalled();
		fixtures.org = { id: "o1" };
		await act(async () => { renderer!.update(<PushManager />); await new Promise((resolve) => setTimeout(resolve, 0)); });
		expect(decideCloudApproval).toHaveBeenCalledExactlyOnceWith({}, "o1", "s1", "apr_1", "deny");
	});
});
