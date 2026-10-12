import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { describe, expect, it, vi } from "vitest";
import { useSidebarNavigation } from "./sidebar-navigation-context";

const motion = vi.hoisted(() => ({ completions: [] as ((result: { finished: boolean }) => void)[], replace: vi.fn(), navigate: vi.fn(), push: vi.fn() }));
const fixture = vi.hoisted(() => ({
	logoRenders: 0,
	allSessions: [{ id: "worker-1", displayName: null as string | null, projectId: "project-1", status: "working", mode: "chat", harness: "codex", isPinned: true, hostId: "host-1", hostName: "Build Laptop", hostAppearance: { symbol: "laptop" } }],
	allProjects: [{ id: "project-1", name: "AO Mobile", hostId: "host-1", hostName: "Build Laptop" }],
	hostStates: [{ hostId: "host-1", connection: "open" }],
}));
const router = { push: motion.push, replace: motion.replace, navigate: motion.navigate };

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });

vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	class Value {
		constructor(_value: number) {}
		interpolate() { return 0; }
		setValue() {}
		stopAnimation(callback?: (value: number) => void) { callback?.(0); }
	}
	return {
		Animated: { Value, View: host("AnimatedView"), spring: () => ({ start: (callback?: (result: { finished: boolean }) => void) => { if (callback) motion.completions.push(callback); } }) },
		BackHandler: { addEventListener: () => ({ remove() {} }) },
		FlatList: ({ data, renderItem, ListEmptyComponent, ...props }: { data: unknown[]; renderItem: (args: { item: unknown }) => React.ReactNode; ListEmptyComponent?: React.ReactNode }) => React.createElement("FlatList", props, data.length ? data.map((item, index) => React.createElement(React.Fragment, { key: index }, renderItem({ item }))) : ListEmptyComponent),
		PanResponder: { create: () => ({ panHandlers: {} }) },
		Pressable: host("Pressable"),
		StyleSheet: { absoluteFill: {}, create: (styles: unknown) => styles, hairlineWidth: 1 },
		Text: host("Text"),
		useWindowDimensions: () => ({ width: 390, height: 844 }),
		View: host("View"),
	};
});
vi.mock("react-native-svg", () => ({ default: "Svg", Path: "Path", Rect: "Rect" }));
vi.mock("expo-router", () => ({ usePathname: () => "/", useRouter: () => router }));
vi.mock("@expo/ui", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return Object.fromEntries(["Button", "Column", "Host", "RNHostView", "Row", "Spacer", "Text"].map(name => [name, host(name)]));
});
vi.mock("react-native-safe-area-context", () => ({ useSafeAreaInsets: () => ({ top: 0, right: 0, bottom: 0, left: 0 }) }));
vi.mock("./AgentLogo", async () => {
	const React = await import("react");
	return { AgentLogo: (props: object) => { fixture.logoRenders++; return React.createElement("AgentLogo", props); } };
});
vi.mock("./icons", async () => {
	const React = await import("react");
	return { Feather: (props: object) => React.createElement("Feather", props) };
});
vi.mock("./sidebar-destination-icon", async () => {
	const React = await import("react");
	return { SidebarDestinationIcon: (props: object) => React.createElement("SidebarDestinationIcon", props) };
});
vi.mock("./sidebar-settings-button", async () => {
	const React = await import("react");
	return { SidebarSettingsButton: (props: object) => React.createElement("SidebarSettingsButton", props) };
});
vi.mock("./sidebar-spawn-button", async () => {
	const React = await import("react");
	return { SidebarSpawnButton: (props: object) => React.createElement("SidebarSpawnButton", props) };
});
vi.mock("./ui", async () => {
	const React = await import("react");
	return { MascotLamp: (props: object) => React.createElement("MascotLamp", props) };
});
vi.mock("./haptics", () => ({ haptics: { tap: vi.fn(), select: vi.fn() } }));
vi.mock("./useReducedMotion", () => ({ useReducedMotion: () => false }));
vi.mock("./ThemeProvider", () => {
	const theme = new Proxy({}, { get: () => "#888" });
	return { useThemeState: () => ({ scheme: "dark" }), useTheme: () => theme, useThemedStyles: (factory: (value: unknown) => unknown) => factory(theme) };
});
vi.mock("./store", () => ({ useApp: () => ({
	allSessions: fixture.allSessions,
	allProjects: fixture.allProjects,
	hostStates: fixture.hostStates,
	connection: "open",
	config: { hostId: "host-1" },
}) }));

import { MachineIdentity } from "./MachineIdentity";
import { SidebarNavigationShell } from "./sidebar-navigation-shell.android";
import { SidebarNavigationShell as IOSSidebarNavigationShell } from "./sidebar-navigation-shell";

function Probe() {
	const { openSidebar, scrollRequest } = useSidebarNavigation();
	return <button onClick={openSidebar} data-sequence={scrollRequest?.sequence ?? 0} />;
}

describe.each([["iOS", IOSSidebarNavigationShell], ["Android", SidebarNavigationShell]])("%s drawer closing", (_platform, Shell) => {
	it("caches the moving page only while the drawer is open or closing", () => {
		motion.completions.length = 0;
		let renderer!: ReactTestRenderer;
		act(() => { renderer = create(<Shell><Probe /></Shell>); });
		const prop = _platform === "iOS" ? "shouldRasterizeIOS" : "renderToHardwareTextureAndroid";
		const cached = () => renderer.root.findAllByType("AnimatedView" as never).some(node => node.props[prop] === true);
		expect(cached()).toBe(false);
		act(() => renderer.root.findByType("button").props.onClick());
		expect(cached()).toBe(true);
		act(() => motion.completions.pop()?.({ finished: true }));
		act(() => renderer.root.findByProps({ testID: "sidebar-projects" }).props.onPress());
		expect(cached()).toBe(true);
		act(() => motion.completions.pop()?.({ finished: true }));
		expect(cached()).toBe(false);
		act(() => renderer.unmount());
	});
	it("clears old reselection scroll requests on navigation while keeping sequences unique", () => {
		let renderer!: ReactTestRenderer;
		act(() => { renderer = create(<Shell><Probe /></Shell>); });
		act(() => renderer.root.findByProps({ testID: "sidebar-agents" }).props.onPress());
		expect(renderer.root.findByType("button").props["data-sequence"]).toBe(1);
		act(() => renderer.root.findByProps({ testID: "sidebar-projects" }).props.onPress());
		expect(renderer.root.findByType("button").props["data-sequence"]).toBe(0);
		act(() => renderer.root.findByProps({ testID: "sidebar-agents" }).props.onPress());
		expect(renderer.root.findByType("button").props["data-sequence"]).toBe(2);
		act(() => renderer.unmount());
	});
	it("retains the default recent-worker scroll buffer", () => {
		let renderer!: ReactTestRenderer;
		act(() => { renderer = create(<Shell><Probe /></Shell>); });
		const list = renderer.root.findByType("FlatList" as never);
		expect(list.props.windowSize).toBeUndefined();
		expect(list.props.maxToRenderPerBatch).toBeUndefined();
		expect(list.props.initialNumToRender).toBeUndefined();
		act(() => renderer.unmount());
	});
	it("still updates a worker's title and offline machine state", () => {
		const sessions = fixture.allSessions;
		const hosts = fixture.hostStates;
		let renderer!: ReactTestRenderer;
		act(() => { renderer = create(<Shell><Probe /></Shell>); });
		fixture.logoRenders = 0;
		fixture.allSessions = [{ ...sessions[0], displayName: "Updated worker" }];
		act(() => renderer.update(<Shell><Probe /></Shell>));
		expect(fixture.logoRenders).toBe(1);
		fixture.hostStates = [{ ...hosts[0], connection: "closed" }];
		act(() => renderer.update(<Shell><Probe /></Shell>));
		expect(renderer.root.findAllByType("Pressable" as never).some(node => node.props.accessibilityLabel?.includes("Updated worker") && node.props.accessibilityLabel?.includes("Build Laptop (offline) machine"))).toBe(true);
		act(() => renderer.unmount());
		fixture.allSessions = sessions;
		fixture.hostStates = hosts;
	});
	it("does not redraw unchanged recent-worker rows on drawer opening or closing completion", () => {
		motion.completions.length = 0;
		let renderer!: ReactTestRenderer;
		act(() => { renderer = create(<Shell><Probe /></Shell>); });
		fixture.logoRenders = 0;
		act(() => renderer.root.findByType("button").props.onClick());
		act(() => motion.completions.pop()?.({ finished: true }));
		act(() => renderer.root.findByProps({ testID: "sidebar-projects" }).props.onPress());
		act(() => motion.completions.pop()?.({ finished: true }));
		expect(fixture.logoRenders).toBe(0);
		act(() => renderer.unmount());
	});
	it("navigates while the drawer closes without waiting or navigating again on completion", () => {
		motion.completions.length = 0;
		motion.replace.mockClear();
		motion.navigate.mockClear();
		let renderer!: ReactTestRenderer;
		act(() => { renderer = create(<Shell><Probe /></Shell>); });
		act(() => { renderer.root.findByType("button").props.onClick(); });
		act(() => { motion.completions.pop()?.({ finished: true }); });
		act(() => { renderer.root.findByProps({ testID: "sidebar-projects" }).props.onPress(); });
		expect(motion.navigate).toHaveBeenCalledExactlyOnceWith("/projects");
		expect(motion.replace).not.toHaveBeenCalled();
		act(() => { motion.completions.pop()?.({ finished: false }); });
		expect(motion.navigate).toHaveBeenCalledExactlyOnceWith("/projects");
		act(() => renderer.unmount());
	});
	it("requests scrolling immediately while retaining clipping until closing finishes", () => {
		motion.completions.length = 0;
		let renderer!: ReactTestRenderer;
		act(() => { renderer = create(<Shell><Probe /></Shell>); });
		act(() => { renderer.root.findByType("button").props.onClick(); });
		act(() => { motion.completions.pop()?.({ finished: true }); });
		act(() => { renderer.root.findByProps({ testID: "sidebar-agents" }).props.onPress(); });
		expect(renderer.root.findByType("button").props["data-sequence"]).toBe(1);
		const isClipped = () => renderer.root.findAllByType("View" as never).some(node => Array.isArray(node.props.style) && node.props.style.some((style: { overflow?: string; borderRadius?: number; borderLeftWidth?: number } | undefined) => _platform === "iOS" ? style?.overflow === "hidden" && style?.borderRadius === 28 : style?.borderLeftWidth === 1));
		expect(isClipped()).toBe(true);
		act(() => { motion.completions.pop()?.({ finished: true }); });
		expect(renderer.root.findByType("button").props["data-sequence"]).toBe(1);
		expect(isClipped()).toBe(false);
		act(() => renderer.unmount());
	});
	it.each(["session", "spawn"])("opens %s immediately rather than after the close animation", (action) => {
		motion.completions.length = 0;
		motion.push.mockClear();
		let renderer!: ReactTestRenderer;
		act(() => { renderer = create(<Shell><Probe /></Shell>); });
		act(() => { renderer.root.findByType("button").props.onClick(); });
		act(() => { motion.completions.pop()?.({ finished: true }); });
		act(() => {
			if (action === "spawn") renderer.root.findByType("SidebarSpawnButton" as never).props.onPress();
			else renderer.root.find(node => node.type === ("Pressable" as never) && node.props.accessibilityLabel?.includes("Build Laptop machine")).props.onPress();
		});
		const target = action === "spawn" ? "/spawn" : {
			pathname: "/session/[id]", params: { id: "worker-1", projectId: "project-1", hostId: "host-1" },
		};
		expect(motion.push).toHaveBeenCalledExactlyOnceWith(target);
		act(() => { motion.completions.pop()?.({ finished: true }); });
		expect(motion.push).toHaveBeenCalledExactlyOnceWith(target);
		act(() => renderer.unmount());
	});
});

describe("Android sidebar recent workers", () => {
	it("shows the machine identity separately from project and status metadata", () => {
		let renderer!: ReactTestRenderer;
		act(() => {
			renderer = create(<SidebarNavigationShell><></></SidebarNavigationShell>);
		});

		const identity = renderer.root.findByType(MachineIdentity);
		expect(identity.props.name).toBe("Build Laptop");
		expect(identity.props.appearance).toEqual({ symbol: "laptop" });
	});

	it("anchors a tilted pin to the harness logo instead of the machine metadata", () => {
		let renderer!: ReactTestRenderer;
		act(() => {
			renderer = create(<SidebarNavigationShell><></></SidebarNavigationShell>);
		});

		const logo = renderer.root.findByProps({ testID: "sidebar-session-logo" });
		const pin = logo.findByProps({ testID: "sidebar-session-pin" });
		const glyph = pin.findByType("Feather" as unknown as React.ElementType);

		expect(glyph.props.fill).toBe("#888");
		expect(pin.props.style).toMatchObject({
			position: "absolute",
			right: -2,
			bottom: -2,
			transform: [{ rotate: "45deg" }],
		});
	});
});
