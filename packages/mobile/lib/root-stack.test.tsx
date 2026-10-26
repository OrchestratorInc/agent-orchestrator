import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
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
vi.mock("@expo-google-fonts/geist/400Regular", () => ({ Geist_400Regular: 1 }));
vi.mock("@expo-google-fonts/geist/500Medium", () => ({ Geist_500Medium: 2 }));
vi.mock("@expo-google-fonts/geist/600SemiBold", () => ({ Geist_600SemiBold: 3 }));
vi.mock("@expo-google-fonts/geist-mono/400Regular", () => ({ GeistMono_400Regular: 4 }));
vi.mock("expo-font", () => ({ useFonts: () => [true, null] }));
vi.mock("expo-splash-screen", () => ({ preventAutoHideAsync: vi.fn(async () => {}), hideAsync: vi.fn(async () => {}) }));
vi.mock("expo-status-bar", () => ({ StatusBar: "StatusBar" }));
vi.mock("react-native-gesture-handler", () => ({ GestureHandlerRootView: "GestureHandlerRootView" }));
vi.mock("react-native-keyboard-controller", () => ({ KeyboardProvider: "KeyboardProvider" }));
vi.mock("react-native-safe-area-context", () => ({ SafeAreaProvider: "SafeAreaProvider" }));
vi.mock("./ThemeProvider", () => ({ ThemeProvider: "ThemeProvider", useTheme: () => fixtures.theme, useThemeState: () => ({ scheme: "dark" }) }));
vi.mock("./themeStore", () => ({ loadThemeSettings: async () => ({ preference: "system", style: "orchestrate" }) }));
vi.mock("./store", () => ({ AppProvider: "AppProvider" }));
vi.mock("./OnboardingGate", () => ({ OnboardingGate: () => null }));
vi.mock("./TelemetryManager", () => ({ TelemetryManager: () => null }));
vi.mock("./PushManager", () => ({ PushManager: () => null }));
vi.mock("./UpdatesManager", () => ({ UpdatesManager: () => null }));
vi.mock("./StoreUpdateManager", () => ({ StoreUpdateManager: () => null }));
vi.mock("./layoutGrid", () => ({ LayoutGrid: () => null }));

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

const pushedPages = ["session/[id]", "review/[sessionId]", "reviewer/[reviewId]", "shell/[handleId]", "preview/[id]", "files/[sessionId]/index", "files/[sessionId]/file"];

describe("root stack", () => {
	// A cold start on a deep link or a notification tap lands with the board
	// under it, so the system back button has somewhere to go.
	it("opens every route above the board", () => {
		expect(unstable_settings).toEqual({ initialRouteName: "(tabs)" });
	});

	it.each(["ios", "android"])("leaves the push transition to the system on %s", async (platform) => {
		const renderer = await renderRoot(platform);
		try {
			const defaults = renderer.root.findByType("Stack" as never).props.screenOptions;
			expect(defaults).not.toHaveProperty("animation");
			expect(defaults.contentStyle).toEqual({ backgroundColor: "#bgBase" });
		} finally {
			act(() => renderer.unmount());
		}
	});

	// No slab on iOS: the bar is the page's own colour. It stays opaque, because
	// a clear bar showed the page underneath through it during every push and
	// pop. Android's top app bar keeps the navigation theme's card surface.
	it("paints the iOS bar in the page's background", async () => {
		const ios = await renderRoot("ios");
		try {
			expect(ios.root.findByType("Stack" as never).props.screenOptions.headerStyle).toEqual({ backgroundColor: "#bgBase" });
			expect(ios.root.findByType("Stack" as never).props.screenOptions).not.toHaveProperty("headerTransparent");
		} finally {
			act(() => ios.unmount());
		}
		const android = await renderRoot("android");
		try {
			expect(android.root.findByType("Stack" as never).props.screenOptions).not.toHaveProperty("headerStyle");
			expect(android.root.findByType("NavigationThemeProvider" as never).props.value.colors.card).toBe("#bgSurface");
		} finally {
			act(() => android.unmount());
		}
	});

	// UIKit draws its own sheet shape when none is named; Android takes Material
	// 3's corner from the token. No sheet picks a radius of its own.
	it("leaves every sheet's shape to the platform", async () => {
		const layout = readFileSync(fileURLToPath(new URL("../app/_layout.tsx", import.meta.url)), "utf8");
		expect(layout).not.toMatch(/sheetCornerRadius:\s*\d/);
		const renderer = await renderRoot("ios");
		try {
			const sheets = renderer.root.findAll((node) => (node.type as unknown) === "StackScreen" && node.props.options?.presentation === "formSheet");
			expect(sheets.length).toBeGreaterThan(10);
			for (const sheet of sheets) {
				expect(sheet.props.options.sheetCornerRadius).toBeUndefined();
				expect(sheet.props.options.sheetAllowedDetents).toBeDefined();
			}
		} finally {
			act(() => renderer.unmount());
		}
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
			const board = (index: number) => ({ name: "(tabs)", state: { index, routes: [{ name: "(workers)" }, { name: "(projects)" }, { name: "(prs)" }] } });
			expect(options("(tabs)").title).toBe("Workers");
			expect(options("(tabs)", board(0)).title).toBe("Workers");
			expect(options("(tabs)", board(1)).title).toBe("Projects");
			expect(options("(tabs)", board(2)).title).toBe("Pull Requests");
			expect(options("(tabs)").headerShown).toBe(false);
			expect(options("notifications")).toMatchObject({ headerShown: false, title: "Notifications" });
			expect(options("project/[id]")).toMatchObject({ headerShown: false, title: "Project" });
		} finally {
			act(() => renderer.unmount());
		}
	});

	it.each(["ios", "android"])("gives every pushed page the system back button on %s", async (platform) => {
		const renderer = await renderRoot(platform);
		try {
			const defaults = renderer.root.findByType("Stack" as never).props.screenOptions;
			expect(defaults.headerBackButtonDisplayMode).toBe("minimal");
			expect(defaults.headerTintColor).toBe("#accent");
			for (const name of pushedPages) {
				const options = renderer.root.findByProps({ name }).props.options;
				expect(options).not.toHaveProperty("headerLeft");
				expect(options).not.toHaveProperty("unstable_headerLeftItems");
				expect(options).not.toHaveProperty("headerBackVisible");
			}
		} finally {
			act(() => renderer.unmount());
		}
	});
});
