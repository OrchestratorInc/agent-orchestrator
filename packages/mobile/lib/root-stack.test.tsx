import { act, create } from "react-test-renderer";
import { afterEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({
	platform: { OS: "ios" },
	theme: new Proxy({}, { get: (_target, property) => `#${String(property)}` }),
}));
vi.mock("react-native", () => ({ Platform: fixtures.platform, View: "View" }));
vi.mock("expo-router", async () => {
	const React = await import("react");
	const Stack = Object.assign(({ children, ...props }: { children?: React.ReactNode }) => React.createElement("Stack", props, children), { Screen: "StackScreen" });
	return { Stack, ThemeProvider: "NavigationThemeProvider", DarkTheme: { colors: {} }, DefaultTheme: { colors: {} } };
});
vi.mock("expo-router/react-navigation", () => ({
	getFocusedRouteNameFromRoute: (route: { state?: { index?: number; routes: { name: string }[] } }) =>
		route.state?.routes[route.state.index ?? 0]?.name,
}));
vi.mock("./appFonts", () => ({ appFonts: {} }));
vi.mock("@expo-google-fonts/geist-mono/400Regular", () => ({ GeistMono_400Regular: 4 }));
vi.mock("expo-font", () => ({ useFonts: () => [true, null] }));
vi.mock("expo-splash-screen", () => ({ preventAutoHideAsync: vi.fn(async () => {}), hideAsync: vi.fn(async () => {}) }));
vi.mock("expo-status-bar", () => ({ StatusBar: "StatusBar" }));
vi.mock("react-native-gesture-handler", () => ({ GestureHandlerRootView: "GestureHandlerRootView" }));
vi.mock("react-native-keyboard-controller", () => ({ KeyboardProvider: "KeyboardProvider" }));
vi.mock("./KeyboardResumeResync", () => ({ KeyboardResumeResync: () => null }));
vi.mock("react-native-safe-area-context", () => ({ SafeAreaProvider: "SafeAreaProvider" }));
vi.mock("./ThemeProvider", () => ({ ThemeProvider: "ThemeProvider", useTheme: () => fixtures.theme, useThemeState: () => ({ scheme: "dark" }) }));
vi.mock("./themeStore", () => ({ loadThemeSettings: async () => ({ preference: "system", style: "orchestrate" }) }));
vi.mock("./store", () => ({ AppProvider: "AppProvider" }));
vi.mock("./cloud/CloudProvider", () => ({ CloudProvider: "CloudProvider" }));
vi.mock("./OnboardingGate", () => ({ OnboardingGate: () => null }));
vi.mock("./TelemetryManager", () => ({ TelemetryManager: () => null }));
vi.mock("./PushManager", () => ({ PushManager: () => null }));
vi.mock("./UpdatesManager", () => ({ UpdatesManager: () => null }));
vi.mock("./StoreUpdateManager", () => ({ StoreUpdateManager: () => null }));

import RootLayout, { unstable_settings } from "../app/_layout";

afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); });

async function renderRoot(platform: string) {
	fixtures.platform.OS = platform;
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	const error = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => {
		if (String(args[0]).startsWith("react-test-renderer is deprecated")) return;
		error(...args);
	});
	let renderer!: ReturnType<typeof create>;
	await act(async () => { renderer = create(<RootLayout />); });
	return renderer;
}

const pushedPages = ["session/[id]", "review/[sessionId]", "reviewer/[reviewId]", "shell/[handleId]", "preview/[id]", "files/[sessionId]/index", "files/[sessionId]/file", "project/[id]"];

describe("root stack", () => {
	// A cold start on a deep link or a notification tap lands with the board
	// under it, so the system back button has somewhere to go.
	it("opens every route above the board", () => {
		expect(unstable_settings).toEqual({ initialRouteName: "(tabs)" });
	});

	// iOS lists the pages under the current one by title in the back button's
	// long-press menu. A route that hides its header still lands there, and
	// without a title it shows as its file name: "(tabs)", "project/[id]".
	it("names every page that hides its header", async () => {
		const renderer = await renderRoot("ios");
		try {
			const options = (name: string, route: object = { name }) => {
				const value = renderer.root.findByProps({ name }).props.options;
				return typeof value === "function" ? value({ route }) : value;
			};
			const board = (index: number) => ({ name: "(tabs)", state: { index, routes: [{ name: "(workers)" }, { name: "(projects)" }] } });
			expect(options("(tabs)").title).toBe("Workers");
			expect(options("(tabs)", board(0)).title).toBe("Workers");
			expect(options("(tabs)", board(1)).title).toBe("Projects");
			expect(options("(tabs)").headerShown).toBe(false);
			// The pair page's back button, pushed from onboarding.
			expect(options("onboarding")).toMatchObject({ title: "Welcome", headerShown: false });
		} finally {
			act(() => renderer.unmount());
		}
	});

	// iOS bars a picker sheet natively from the start, in the grouped colour of
	// the list under it; Android keeps the title in the sheet's body.
	it.each(["ios", "android"])("titles the picker sheets in a native bar only on iOS, on %s", async (platform) => {
		const renderer = await renderRoot(platform);
		try {
			for (const [name, title] of [["sheets/agent", "Agent"], ["sheets/model", "Model"]]) {
				expect(renderer.root.findByProps({ name }).props.options).toMatchObject({ presentation: "formSheet", title, headerShown: platform === "ios" });
			}
		} finally {
			act(() => renderer.unmount());
		}
	});

	it.each(["ios", "android"])("gives every pushed page the system back button and transition on %s", async (platform) => {
		const renderer = await renderRoot(platform);
		try {
			expect(renderer.root.findByType("Stack" as never).props.screenOptions).not.toHaveProperty("animation");
			for (const name of pushedPages) {
				const options = renderer.root.findByProps({ name }).props.options;
				expect(options).not.toHaveProperty("headerLeft");
				expect(options).not.toHaveProperty("unstable_headerLeftItems");
				expect(options).not.toHaveProperty("headerBackVisible");
				expect(options.headerShown).not.toBe(false);
			}
			// Titled before the page renames itself to the project.
			expect(renderer.root.findByProps({ name: "project/[id]" }).props.options.title).toBe("Project");
			// Settings, Notifications and Pull Requests are pages pushed over the
			// board on both platforms, each headed by its own stack's bar.
			for (const [name, title] of [["settings", "Settings"], ["notifications", "Notifications"], ["prs", "Pull Requests"]]) {
				const options = renderer.root.findByProps({ name }).props.options;
				expect(options).toMatchObject({ title, headerShown: false });
				for (const key of ["presentation", "animation", "gestureEnabled", "sheetAllowedDetents", "sheetGrabberVisible"]) expect(options).not.toHaveProperty(key);
			}
		} finally {
			act(() => renderer.unmount());
		}
	});
});
