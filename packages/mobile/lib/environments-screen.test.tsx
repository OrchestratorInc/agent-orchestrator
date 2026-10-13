import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Host } from "./hosts";

const fixtures = vi.hoisted(() => ({
	loadHosts: vi.fn<() => Promise<Host[]>>(async () => []),
	activeHost: vi.fn<() => Promise<Host | null>>(async () => null),
	reloadConfig: vi.fn(),
	replace: vi.fn(),
	push: vi.fn(),
	platform: { OS: "ios" },
	theme: new Proxy({}, { get: () => "#000000" }),
}));

vi.mock("expo-router", async () => {
	const React = await import("react");
	return {
		useFocusEffect: (effect: () => void) => React.useEffect(effect, [effect]),
		useRouter: () => ({ push: fixtures.push, replace: fixtures.replace }),
	};
});
vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) =>
		React.createElement(name, props, children);
	return {
		ActivityIndicator: host("ActivityIndicator"),
		Alert: { alert: vi.fn() },
		Platform: fixtures.platform,
		Pressable: host("Pressable"),
		RefreshControl: host("RefreshControl"),
		ScrollView: host("ScrollView"),
		StyleSheet: { create: (styles: object) => styles, hairlineWidth: 1 },
		Text: host("Text"),
		View: host("View"),
	};
});
vi.mock("react-native-keyboard-controller", () => ({
	KeyboardAvoidingView: "KeyboardAvoidingView",
	KeyboardAwareScrollView: "KeyboardAwareScrollView",
}));
vi.mock("react-native-safe-area-context", () => ({ useSafeAreaInsets: () => ({ bottom: 24 }) }));
vi.mock("../lib/api", () => ({ ApiError: class ApiError extends Error {}, pingServer: vi.fn() }));
vi.mock("../lib/connectionError", () => ({ classifyConnectionFailure: vi.fn(), describeConnectionFailure: vi.fn() }));
vi.mock("../lib/connectRuntime", () => ({ probeEndpoint: vi.fn() }));
vi.mock("../lib/disconnect", () => ({ forgetPairedHost: vi.fn(), forgetServer: vi.fn() }));
vi.mock("../lib/EnvironmentMachineRow", async () => {
	const React = await import("react");
	return {
		EnvironmentMachineRow: (props: object) => React.createElement("EnvironmentMachineRow", props),
		EnvironmentEditorActions: (props: object) => React.createElement("EnvironmentEditorActions", props),
	};
});
vi.mock("../lib/haptics", () => ({ haptics: { error: vi.fn(), success: vi.fn(), select: vi.fn(), tap: vi.fn() } }));
vi.mock("../lib/hosts", () => ({
	activeHost: fixtures.activeHost,
	activeHostMetadata: fixtures.activeHost,
	loadHosts: fixtures.loadHosts,
	renameHost: vi.fn(),
	saveHost: vi.fn(),
	updateHostAppearance: vi.fn(),
}));
vi.mock("../lib/icons", async () => {
	const React = await import("react");
	return { Feather: (props: object) => React.createElement("Feather", props) };
});
vi.mock("../lib/manualConnect", () => ({ DifferentMachineError: class DifferentMachineError extends Error {}, persistManualHostEdit: vi.fn() }));
vi.mock("../lib/native-header-button", async () => {
	const React = await import("react");
	return { NativeHeaderButton: (props: object) => React.createElement("NativeHeaderButton", props) };
});
vi.mock("../lib/race", () => ({ IncompatibleHostVersionError: class IncompatibleHostVersionError extends Error {} }));
vi.mock("../lib/store", () => ({
	useApp: () => ({
		hostStates: [], configForHost: vi.fn(), switchHost: vi.fn(), refreshAll: vi.fn(),
		reloadConfig: fixtures.reloadConfig, reloadHostMetadata: vi.fn(async () => {}),
	}),
}));
vi.mock("../lib/ThemeProvider", () => ({
	useTheme: () => fixtures.theme,
	useThemedStyles: (factory: (theme: object) => object) => factory(fixtures.theme),
}));
vi.mock("../lib/backNavigation", () => ({ backOr: vi.fn() }));
vi.mock("../lib/RouteErrorBoundary", () => ({ RouteErrorBoundary: () => null }));

import EnvironmentsScreen from "../app/settings/environments";
import { forgetPairedHost, forgetServer } from "./disconnect";
import { Alert } from "react-native";

let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixtures.loadHosts.mockReset().mockResolvedValue([]);
	fixtures.activeHost.mockReset().mockResolvedValue(null);
	fixtures.reloadConfig.mockReset();
	fixtures.replace.mockClear();
	fixtures.push.mockClear();
	vi.mocked(forgetServer).mockReset().mockResolvedValue(undefined);
	vi.mocked(forgetPairedHost).mockReset().mockResolvedValue(undefined);
	vi.mocked(Alert.alert).mockClear();
	fixtures.platform.OS = "ios";
	const error = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => {
		if (String(args[0]).startsWith("react-test-renderer is deprecated")) return;
		error(...args);
	});
});

afterEach(async () => {
	if (renderer) await act(async () => renderer?.unmount());
	renderer = undefined;
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
});

describe("Environments editor keyboard handling", () => {
	it.each(["ios", "android"])("keeps focused fields and the fixed actions above the %s keyboard", async (platform) => {
		fixtures.platform.OS = platform;
		await act(async () => {
			renderer = create(<EnvironmentsScreen />);
			await Promise.resolve();
			await Promise.resolve();
		});

		const avoiding = renderer!.root.findByType("KeyboardAvoidingView" as never);
		expect(avoiding.props.behavior).toBe("padding");
		expect(avoiding.props.automaticOffset).toBe(true);
		const scroll = renderer!.root.findByType("KeyboardAwareScrollView" as never);
		expect(scroll.props.automaticallyAdjustKeyboardInsets).toBe(false);
		expect(scroll.props.enabled).not.toBe(false);
		expect(scroll.props.keyboardDismissMode).toBe("interactive");
	});

	it("places edit actions outside the scroll area and ignores an old editor closing", async () => {
		const a: Host = { id: "a", name: "A", platform: "darwin", endpoints: [], token: "token", lastConnected: 1 };
		fixtures.loadHosts.mockResolvedValue([a, { ...a, id: "b", name: "B" }]);
		fixtures.activeHost.mockResolvedValue(a);
		await act(async () => { renderer = create(<EnvironmentsScreen />); });
		const [first, second] = renderer!.root.findAllByType("EnvironmentMachineRow" as never);
		const editor = { hostId: "a", canSave: true, saving: false, onSave: vi.fn(), onCancel: vi.fn() };
		act(() => first.props.onEditorStateChange(editor, "a"));
		const scroll = renderer!.root.findByType("KeyboardAwareScrollView" as never);
		expect(scroll.findAllByType("EnvironmentEditorActions" as never)).toHaveLength(0);
		expect(renderer!.root.findByType("EnvironmentEditorActions" as never).props.editor.hostId).toBe("a");
		act(() => {
			second.props.onExpandedChange(true);
			second.props.onEditorStateChange({ ...editor, hostId: "b" }, "b");
			first.props.onEditorStateChange(null, "a");
		});
		expect(renderer!.root.findByType("EnvironmentEditorActions" as never).props.editor.hostId).toBe("b");
		act(() => second.props.onEditorStateChange(null, "b"));
		expect(renderer!.root.findAllByType("EnvironmentEditorActions" as never)).toHaveLength(0);
	});
});

describe("forgetting environments", () => {
	it.each([
		["ios", false], ["ios", true], ["android", false], ["android", true],
	] as const)("updates the cards before reconnecting the next default on %s (last machine: %s)", async (platform, lastMachine) => {
		fixtures.platform.OS = platform;
		const a: Host = { id: "a", name: "A", platform: "darwin", endpoints: [], token: "a-token", lastConnected: 2 };
		const b: Host = { ...a, id: "b", name: "B", token: "b-token", lastConnected: 1 };
		fixtures.loadHosts.mockResolvedValue(lastMachine ? [a] : [a, b]);
		fixtures.activeHost.mockResolvedValue(a);
		await act(async () => { renderer = create(<EnvironmentsScreen />); });
		fixtures.loadHosts.mockResolvedValue(lastMachine ? [] : [b]);
		// A is still selected when Forget reads storage; B (or nothing) afterwards.
		fixtures.activeHost.mockResolvedValueOnce(a).mockResolvedValue(lastMachine ? null : b);
		let release!: () => void;
		fixtures.reloadConfig.mockReturnValue(new Promise<void>((resolve) => { release = resolve; }));
		vi.mocked(forgetServer).mockResolvedValue(undefined);
		await act(async () => {
			void renderer!.root.findAllByType("EnvironmentMachineRow" as never)[0].props.onForget();
			await new Promise((resolve) => setTimeout(resolve, 0));
		});
		try {
			expect(fixtures.replace).not.toHaveBeenCalled();
			expect(renderer!.root.findByProps({ testID: "environment-forgotten-notice" }).props.children).toBe(lastMachine ? "A forgotten." : "A forgotten. B is now the default.");
			if (lastMachine) {
				expect(renderer!.root.findAllByType("EnvironmentMachineRow" as never)).toHaveLength(0);
				act(() => renderer!.root.findByProps({ testID: "environment-add-machine" }).props.onPress());
				expect(fixtures.push).toHaveBeenCalledWith("/pair");
			} else {
				const rows = renderer!.root.findAllByType("EnvironmentMachineRow" as never);
				expect(rows.map((row) => row.props.host.id)).toEqual(["b"]);
				expect(rows[0].props.isDefault).toBe(true);
				expect(rows[0].props.expanded).toBe(false);
			}
		} finally {
			await act(async () => { release(); });
		}
	});

	it("keeps a machine visible and reports failure if its saved connection remains", async () => {
		const a: Host = { id: "a", name: "A", platform: "darwin", endpoints: [], token: "token", lastConnected: 1 };
		fixtures.loadHosts.mockResolvedValue([a]);
		fixtures.activeHost.mockResolvedValue(a);
		vi.mocked(forgetServer).mockRejectedValue(new Error("storage failed"));
		await act(async () => { renderer = create(<EnvironmentsScreen />); });
		await act(async () => { await renderer!.root.findByType("EnvironmentMachineRow" as never).props.onForget(); });
		expect(renderer!.root.findByType("EnvironmentMachineRow" as never).props.host.id).toBe("a");
		expect(Alert.alert).toHaveBeenCalledWith("Couldn't forget machine", expect.any(String));
		expect(renderer!.root.findAllByProps({ testID: "environment-forgotten-notice" })).toHaveLength(0);
	});

	it("forgets a non-default without changing the default or reconnecting", async () => {
		const a: Host = { id: "a", name: "A", platform: "darwin", endpoints: [], token: "token", lastConnected: 1 };
		const b = { ...a, id: "b", name: "B" };
		fixtures.loadHosts.mockResolvedValue([a, b]);
		fixtures.activeHost.mockResolvedValue(a);
		await act(async () => { renderer = create(<EnvironmentsScreen />); });
		fixtures.loadHosts.mockResolvedValue([a]);
		await act(async () => { await renderer!.root.findAllByType("EnvironmentMachineRow" as never)[1].props.onForget(); });
		expect(forgetPairedHost).toHaveBeenCalledWith(b);
		expect(forgetServer).not.toHaveBeenCalled();
		expect(fixtures.reloadConfig).not.toHaveBeenCalled();
		expect(renderer!.root.findByType("EnvironmentMachineRow" as never).props.isDefault).toBe(true);
		expect(renderer!.root.findByProps({ testID: "environment-forgotten-notice" }).props.children).toBe("B forgotten.");
	});

	it("forgets the tapped machine even when the selection changed while the screen was open", async () => {
		const a: Host = { id: "a", name: "A", platform: "darwin", endpoints: [], token: "token", lastConnected: 2 };
		const b = { ...a, id: "b", name: "B", lastConnected: 1 };
		fixtures.loadHosts.mockResolvedValue([a, b]);
		fixtures.activeHost.mockResolvedValue(a);
		await act(async () => { renderer = create(<EnvironmentsScreen />); });
		// Account sync moved the default to B; this screen still shows A as default.
		fixtures.activeHost.mockResolvedValue(b);
		fixtures.loadHosts.mockResolvedValue([b]);
		await act(async () => { await renderer!.root.findAllByType("EnvironmentMachineRow" as never)[0].props.onForget(); });
		expect(forgetServer).not.toHaveBeenCalled();
		expect(forgetPairedHost).toHaveBeenCalledWith(a);
		expect(fixtures.reloadConfig).not.toHaveBeenCalled();
		expect(renderer!.root.findByType("EnvironmentMachineRow" as never).props.isDefault).toBe(true);
		expect(renderer!.root.findByProps({ testID: "environment-forgotten-notice" }).props.children).toBe("A forgotten.");
	});

	it("passes the tapped default to forgetServer instead of re-reading the selection", async () => {
		const a: Host = { id: "a", name: "A", platform: "darwin", endpoints: [], token: "token", lastConnected: 1 };
		fixtures.loadHosts.mockResolvedValue([a]);
		fixtures.activeHost.mockResolvedValue(a);
		await act(async () => { renderer = create(<EnvironmentsScreen />); });
		fixtures.loadHosts.mockResolvedValue([]);
		fixtures.reloadConfig.mockResolvedValue(undefined);
		await act(async () => { await renderer!.root.findByType("EnvironmentMachineRow" as never).props.onForget(); });
		expect(forgetServer).toHaveBeenCalledWith(a);
		expect(forgetPairedHost).not.toHaveBeenCalled();
	});
});
