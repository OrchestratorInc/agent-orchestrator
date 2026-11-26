import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({
	navigate: vi.fn(),
	push: vi.fn(),
	back: vi.fn(),
	alert: vi.fn(),
	prompt: vi.fn(),
	openOrStartUpdate: vi.fn(async () => "store"),
	checkStore: vi.fn(async () => null as unknown),
	setPreference: vi.fn(),
	select: vi.fn(),
	account: null as { email: string; name?: string; avatarUrl?: string } | null,
	signOut: vi.fn(async () => {}),
	clearAccountHosts: vi.fn(async () => {}),
	reloadConfig: vi.fn(async (): Promise<unknown> => null),
	hostStates: [] as { hostId: string; connection: "closed" | "connecting" | "open"; loading: boolean }[],
	platform: { OS: "android" },
	hosts: [] as { id: string; name: string; endpoints: never[]; token: string; platform: string; lastConnected: number }[],
	theme: new Proxy({}, { get: () => "#000000" }),
	config: null as { host: string; httpPort: string } | null,
	pushStatus: { supported: true, granted: false, canAskAgain: true, registered: false },
	emulating: false,
	setEmulated: vi.fn(),
}));
vi.mock("./native-header-button", async () => {
	const React = await import("react");
	return { NativeHeaderButton: (props: object) => React.createElement("NativeHeaderButton", props) };
});
vi.mock("expo-router", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	const router = { navigate: fixtures.navigate, push: fixtures.push };
	return {
		Stack: { Toolbar: Object.assign(host("StackToolbar"), { Button: host("StackToolbarButton"), View: host("StackToolbarView") }) },
		useRouter: () => router,
		useFocusEffect: (effect: () => void) => React.useEffect(effect, [effect]),
	};
});
vi.mock("react-native", () => ({
	Alert: { alert: fixtures.alert }, Linking: { openURL: vi.fn(async () => {}) },
	Platform: fixtures.platform, StyleSheet: { create: (styles: object) => styles }, View: "View",
}));
// The rows are the native form's (lib/SettingsForm); here they stay as hosts so
// the screen's structure and wiring can be read off their props.
vi.mock("./SettingsForm", () => ({
	SettingsForm: "SettingsForm",
	SettingsSection: "SettingsSection",
	SettingsLinkRow: "SettingsLinkRow",
	SettingsValueRow: "SettingsValueRow",
	SettingsButtonRow: "SettingsButtonRow",
	SettingsToggleRow: "SettingsToggleRow",
	SettingsPickerRow: "SettingsPickerRow",
	SettingsStatusRow: "SettingsStatusRow",
	SettingsAccountRow: "SettingsAccountRow",
}));
vi.mock("./AccountAvatar", () => ({
	AccountAvatar: "AccountAvatar",
	accountName: (account: { name?: string; email: string }) => account.name || account.email,
}));
vi.mock("./Toast", () => ({ ToastHost: "ToastHost" }));
vi.mock("./emulatedMachines", () => ({
	isEmulatedHost: (id: string) => id.startsWith("emulated-"),
	setEmulatedMachines: fixtures.setEmulated,
	useEmulatedMachines: () => fixtures.emulating,
}));
vi.mock("./Spinner", () => ({ Spinner: "Spinner" }));
vi.mock("expo-application", () => ({ nativeApplicationVersion: "1.0", nativeBuildVersion: "1" }));
vi.mock("expo-clipboard", () => ({ setStringAsync: vi.fn() }));
vi.mock("expo-device", () => ({ modelName: "Phone" }));
vi.mock("expo-constants", () => ({ default: { expoConfig: {} } }));
vi.mock("expo-updates", () => ({ isEnabled: false, updateId: null, channel: null, runtimeVersion: "1", useUpdates: () => ({}) }));
vi.mock("./account", () => ({ loadAccount: async () => fixtures.account, signInToAccount: vi.fn(), signOutOfAccount: fixtures.signOut }));
vi.mock("./accountHosts", () => ({ clearAccountHosts: fixtures.clearAccountHosts, syncAccountHosts: vi.fn() }));
// Signed out of Orchestrator Cloud: its row stays out of the machine list. Its
// sign-out is the account's, then forgetting the account's machines.
vi.mock("./cloud/CloudProvider", () => ({ useCloud: () => ({
	status: "signed-out", connections: [], connectionsLoaded: false,
	signOut: async () => { await fixtures.signOut(); await fixtures.clearAccountHosts(); },
}) }));
vi.mock("./config", () => ({ isConfigured: () => false }));
vi.mock("./hosts", () => ({ activeHost: async () => fixtures.hosts[0], loadHosts: async () => fixtures.hosts }));
vi.mock("./haptics", () => ({ haptics: { tap: vi.fn(), select: fixtures.select, success: vi.fn(), error: vi.fn() } }));
vi.mock("./inAppUpdates", () => ({ checkStore: fixtures.checkStore, openOrStartUpdate: fixtures.openOrStartUpdate }));
vi.mock("./storeUpdatePrompt", () => ({ promptStoreUpdate: fixtures.prompt }));
vi.mock("./openGitHub", () => ({ openGitHub: vi.fn() }));
vi.mock("./push", () => ({
	getPushStatus: async () => fixtures.pushStatus,
	openNotificationSettings: vi.fn(), registerForPush: vi.fn(), unregisterFromPush: vi.fn(),
}));
vi.mock("./store", () => ({
	useApp: () => ({
		config: fixtures.config, configured: false, selectedHostName: "Dev", connection: "closed", activeEndpoints: [],
		hostStates: fixtures.hostStates, reloadConfig: fixtures.reloadConfig,
	}),
}));
vi.mock("./ThemeProvider", () => ({
	useTheme: () => fixtures.theme,
	useThemeState: () => ({ preference: "system", scheme: "dark", style: "orchestrate", setPreference: fixtures.setPreference }),
}));
vi.mock("./updates", () => ({ checkAndDownload: vi.fn(async () => ({ kind: "up-to-date" })), describeUpdateRow: () => ({ value: "Check now", tone: "default", busy: false, action: "check" }) }));
vi.mock("./backNavigation", () => ({ backOr: fixtures.back }));
vi.mock("./RouteErrorBoundary", () => ({ RouteErrorBoundary: () => null }));

import SettingsScreen from "../app/settings/index";
import { postEnvironmentNotice, takeEnvironmentNotice } from "./environmentNotice";

const DEV = { id: "dev", name: "Dev", endpoints: [], token: "token", platform: "darwin", lastConnected: 1 };
const LAB = { ...DEV, id: "lab", name: "Lab", lastConnected: 2 };

let renderer: ReactTestRenderer | undefined;
beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	vi.stubGlobal("__DEV__", false);
	for (const mock of [fixtures.navigate, fixtures.push, fixtures.back, fixtures.alert, fixtures.prompt, fixtures.openOrStartUpdate, fixtures.checkStore, fixtures.setPreference, fixtures.select, fixtures.signOut, fixtures.clearAccountHosts]) mock.mockClear();
	fixtures.reloadConfig.mockClear();
	fixtures.setEmulated.mockClear();
	fixtures.emulating = false;
	fixtures.reloadConfig.mockImplementation(async () => null);
	fixtures.hostStates = [];
	fixtures.hosts = [DEV];
	fixtures.account = null;
	fixtures.config = null;
	fixtures.pushStatus = { supported: true, granted: false, canAskAgain: true, registered: false };
	fixtures.platform.OS = "android";
	takeEnvironmentNotice();
	const error = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => {
		if (String(args[0]).startsWith("react-test-renderer is deprecated")) return;
		error(...args);
	});
});
afterEach(() => {
	act(() => renderer?.unmount());
	vi.unstubAllGlobals();
	vi.restoreAllMocks();
});

async function renderSettings() {
	await act(async () => { renderer = create(<SettingsScreen />); });
	return renderer!.root;
}

const ROW_TYPES = ["SettingsLinkRow", "SettingsValueRow", "SettingsButtonRow", "SettingsToggleRow", "SettingsPickerRow", "SettingsStatusRow"];

function rowsOf(section: ReactTestInstance) {
	return section.findAll((node) => ROW_TYPES.includes(node.type as unknown as string));
}

function section(root: ReactTestInstance, title: string) {
	return root.find((node) => (node.type as unknown) === "SettingsSection" && node.props.title === title);
}

function row(root: ReactTestInstance, label: string) {
	return root.find((node) => ROW_TYPES.includes(node.type as unknown as string) && node.props.label === label);
}

describe("Settings", () => {
	it("has no Developer section in a release build", async () => {
		const root = await renderSettings();
		expect(root.findAllByType("SettingsSection" as never).map((node) => node.props.title)).toEqual(["Account", "Machines", "Preferences", "Updates", "Support"]);
	});

	// Last, and saying plainly that it is only in development builds.
	it("ends a development build with the Emulate more machines switch", async () => {
		vi.stubGlobal("__DEV__", true);
		const root = await renderSettings();
		const sections = root.findAllByType("SettingsSection" as never);
		const developer = sections.at(-1)!;
		expect(developer.props.title).toBe("Developer");
		expect(developer.props.footer).toMatch(/^Development builds only\./);
		const toggle = row(root, "Emulate more machines");
		expect(toggle.props.value).toBe(false);
		act(() => toggle.props.onValueChange(true));
		expect(fixtures.setEmulated).toHaveBeenCalledWith(true);
	});

	// A fake has no page: its row says what it is instead of opening one.
	it("lists the fake machines after the real ones, without a page to open", async () => {
		vi.stubGlobal("__DEV__", true);
		fixtures.emulating = true;
		fixtures.hostStates = [{ hostId: "emulated-1", name: "Gaming PC", connection: "closed", loading: false, appearance: { symbol: "desktop" } } as never];
		const root = await renderSettings();
		expect(rowsOf(section(root, "Machines")).map((node) => node.props.label)).toEqual(["Dev", "Gaming PC", "Scan new code", "Add a machine"]);
		// With a fake beside it, the real machine says it is the default.
		expect(row(root, "Dev").props.status).toBe("Unreachable · Default");
		const fake = row(root, "Gaming PC");
		expect(fake.props.status).toBe("Offline");
		act(() => fake.props.onPress());
		expect(fixtures.push).not.toHaveBeenCalled();
		expect(root.findByType("ToastHost" as never).props.toast.current.text).toMatch(/fake machine for this development build/);
	});

	// The fixture's default machine has no working config, so its repair row shows.
	it("lists each machine once, then the machine actions", async () => {
		const root = await renderSettings();
		const machines = section(root, "Machines");
		expect(rowsOf(machines).map((node) => [node.type, node.props.label])).toEqual([
			["SettingsStatusRow", "Dev"],
			["SettingsButtonRow", "Scan new code"],
			["SettingsButtonRow", "Add a machine"],
		]);
		// One machine is the default without saying so.
		expect(row(root, "Dev").props).toMatchObject({ status: "Unreachable", tone: "bad" });
		expect(row(root, "Scan new code").props.value).toBeUndefined();

		act(() => row(root, "Add a machine").props.onPress());
		expect(fixtures.navigate).toHaveBeenCalledWith("/pair");
		act(() => row(root, "Scan new code").props.onPress());
		expect(fixtures.push).toHaveBeenCalledWith({ pathname: "/pair", params: { repairHostId: "dev" } });
	});

	// Testing, making default, reconnecting, editing and forgetting are all on
	// the machine's page.
	it("opens a machine's page from its row", async () => {
		const root = await renderSettings();
		act(() => row(root, "Dev").props.onPress());
		expect(fixtures.push).toHaveBeenCalledWith({ pathname: "/settings/machine/[id]", params: { id: "dev" } });
	});

	it("marks the default only beside another machine, and names it on its repair row", async () => {
		fixtures.hosts = [DEV, LAB];
		fixtures.hostStates = [{ hostId: "lab", connection: "open", loading: false }];
		const root = await renderSettings();
		expect(row(root, "Dev").props).toMatchObject({ status: "Unreachable · Default", tone: "bad" });
		expect(row(root, "Lab").props).toMatchObject({ status: "Connected", tone: "good" });
		expect(row(root, "Scan new code").props.value).toBe("Dev");
	});

	// A phone paired before machines were saved has only the saved config.
	it("stands the saved config in as the one machine when none are saved", async () => {
		fixtures.hosts = [];
		const root = await renderSettings();
		expect(rowsOf(section(root, "Machines")).map((node) => [node.type, node.props.label])).toEqual([
			["SettingsValueRow", "Dev"],
			["SettingsButtonRow", "Add a machine"],
		]);
		expect(row(root, "Dev").props).toMatchObject({ value: "Unreachable", tone: "bad" });
	});

	// Its page goes back here when a machine is forgotten, and leaves word of it.
	it("says what happened to a machine forgotten on its page", async () => {
		postEnvironmentNotice("Lab forgotten. Dev is now the default.");
		const root = await renderSettings();
		expect(root.findByType("ToastHost" as never).props.toast.current).toMatchObject({ text: "Lab forgotten. Dev is now the default." });
	});

	it("opens Theme as a page from its link row", async () => {
		const root = await renderSettings();
		const theme = row(root, "Theme");
		expect(theme.props.value).toBe("Orchestrate");
		act(() => theme.props.onPress());
		expect(fixtures.push).toHaveBeenCalledWith("/settings/theme");
	});

	// Light, dark or system stays its own row beside Theme.
	it("picks light, dark or system from the Appearance row", async () => {
		const root = await renderSettings();
		const appearance = row(root, "Appearance");
		expect(appearance.props.selectedValue).toBe("system");
		act(() => appearance.props.onValueChange("dark"));
		expect(fixtures.setPreference).toHaveBeenCalledWith("dark");
	});

	it("confirms copied report details in a toast instead of on the row", async () => {
		const root = await renderSettings();
		const report = row(root, "Report a problem");
		act(() => report.props.onPress());
		expect(row(root, "Report a problem").props.value).toBeUndefined();
		expect(root.findByType("ToastHost" as never).props.toast.current.text).toMatch(/^Details copied/);
	});

	// Notifications are on by default, so the row is only there to turn them
	// back on.
	it("offers the notifications toggle only while they are off", async () => {
		fixtures.config = { host: "10.0.0.2", httpPort: "3011" };
		let root = await renderSettings();
		expect(row(root, "Notifications").props.value).toBe(false);

		fixtures.pushStatus = { supported: true, granted: true, canAskAgain: true, registered: true };
		act(() => renderer?.unmount());
		root = await renderSettings();
		expect(root.findAll((node) => (node.type as unknown) === "SettingsToggleRow" && node.props.label === "Notifications")).toHaveLength(0);

		// Nothing paired: no machine to register with.
		fixtures.config = null;
		fixtures.pushStatus = { supported: true, granted: false, canAskAgain: true, registered: false };
		act(() => renderer?.unmount());
		root = await renderSettings();
		expect(root.findAll((node) => (node.type as unknown) === "SettingsToggleRow" && node.props.label === "Notifications")).toHaveLength(0);
	});

	it("keeps removal and editing on each machine's page", async () => {
		const root = await renderSettings();
		const labels = root.findAll((node) => ROW_TYPES.includes(node.type as unknown as string)).map((node) => node.props.label as string);
		for (const label of labels) expect(label).not.toMatch(/Forget|Disconnect|Edit|Rename/);
	});

	it("raises the store's own update prompt when a manual check finds a native release", async () => {
		fixtures.checkStore.mockResolvedValueOnce({ updateAvailable: true, storeVersion: "9.9.9" });
		const root = await renderSettings();

		await act(async () => { row(root, "Software update").props.onPress(); });
		expect(fixtures.prompt).toHaveBeenCalledOnce();
		const [prompt] = fixtures.prompt.mock.calls[0] as unknown as [{ storeConfirmed: boolean; onUpdate(): void }];
		expect(prompt.storeConfirmed).toBe(true);
		prompt.onUpdate();
		expect(fixtures.openOrStartUpdate).toHaveBeenCalledWith({ updateAvailable: true, storeVersion: "9.9.9" });
	});

	// First on the page, a plain Sign in row, and no footer explaining it.
	it("leads with the account, asking a signed-out phone to sign in", async () => {
		fixtures.account = null;
		const root = await renderSettings();
		const account = root.findAllByType("SettingsSection" as never)[0];
		expect(account.props.title).toBe("Account");
		expect(account.props.footer).toBeUndefined();
		expect(row(root, "Sign in to Orchestrator")).toBeTruthy();
	});

	// As the desktop's account row: the photo, the person's name, the email under
	// it, and Sign out below.
	it("shows the signed-in person with their photo, and Sign out below", async () => {
		fixtures.account = { email: "me@example.com", name: "Prasad Ware", avatarUrl: "https://example.com/me.png" };
		const root = await renderSettings();
		const account = root.findAllByType("SettingsSection" as never)[0];
		expect(account.props.footer).toBeUndefined();
		const person = account.findByType("SettingsAccountRow" as never);
		expect(person.props).toMatchObject({ name: "Prasad Ware", email: "me@example.com" });
		expect(person.props.avatar.props.account).toMatchObject({ avatarUrl: "https://example.com/me.png" });
		const rows = account.findAll((node) => ["SettingsAccountRow", "SettingsButtonRow"].includes(node.type as unknown as string));
		expect(rows.map((node) => node.props.label ?? node.props.name)).toEqual(["Prasad Ware", "Sign out"]);
	});

	// Signing out forgets every machine the account synced here, so it asks first.
	it("confirms before signing out and removing synced machines", async () => {
		fixtures.account = { email: "me@example.com" };
		const root = await renderSettings();
		// No name from the provider: the email is the name, not repeated under it.
		expect(root.findByType("SettingsAccountRow" as never).props).toMatchObject({ name: "me@example.com", email: undefined });
		const signOut = row(root, "Sign out");
		expect(signOut.props.destructive).toBe(true);

		act(() => signOut.props.onPress());
		expect(fixtures.signOut).not.toHaveBeenCalled();
		const [title, , buttons] = fixtures.alert.mock.calls.at(-1)!;
		expect(title).toBe("Sign out?");
		expect(buttons.map((button: { text: string; style?: string }) => [button.text, button.style])).toEqual([["Cancel", "cancel"], ["Sign out", "destructive"]]);

		await act(async () => { buttons[1].onPress(); });
		expect(fixtures.signOut).toHaveBeenCalledOnce();
		expect(fixtures.clearAccountHosts).toHaveBeenCalledOnce();
	});

	// Settings is the first page of its own stack on iOS, so it adds the back
	// chevron UIKit wouldn't draw there (lib/StackRootBack). Android's header
	// has its own.
	it.each(["ios", "android"])("leaves Settings by the header's back button on %s", async (platform) => {
		fixtures.platform.OS = platform;
		const root = await renderSettings();
		const buttons = root.findAllByType("NativeHeaderButton" as never);
		expect(buttons.map((button) => button.props.label)).toEqual(platform === "ios" ? ["Back"] : []);
		if (platform === "ios") {
			act(() => buttons[0].props.onPress());
			expect(fixtures.back).toHaveBeenCalledOnce();
		}
	});
});
