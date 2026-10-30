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
vi.mock("./appFonts", () => ({ appFonts: {} }));
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

const pushedPages = ["session/[id]", "review/[sessionId]", "reviewer/[reviewId]", "shell/[handleId]", "preview/[id]", "files/[sessionId]/index", "files/[sessionId]/file", "project/[id]"];

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

	// No slab on either platform: the bar is the page's own colour. It stays
	// opaque, because a clear bar showed the page underneath through it during
	// every push and pop.
	it("paints the bar in the page's background", async () => {
		const ios = await renderRoot("ios");
		try {
			expect(ios.root.findByType("Stack" as never).props.screenOptions.headerStyle).toEqual({ backgroundColor: "#bgBase" });
			expect(ios.root.findByType("Stack" as never).props.screenOptions).not.toHaveProperty("headerTransparent");
		} finally {
			act(() => ios.unmount());
		}
		const android = await renderRoot("android");
		try {
			expect(android.root.findByType("Stack" as never).props.screenOptions.headerStyle).toEqual({ backgroundColor: "#bgBase" });
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
			const board = (index: number) => ({ name: "(tabs)", state: { index, routes: [{ name: "(workers)" }, { name: "(projects)" }] } });
			expect(options("(tabs)").title).toBe("Workers");
			expect(options("(tabs)", board(0)).title).toBe("Workers");
			expect(options("(tabs)", board(1)).title).toBe("Projects");
			expect(options("(tabs)").headerShown).toBe(false);
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
				expect(renderer.root.findByProps({ name }).props.options).toMatchObject({
					presentation: "formSheet",
					title,
					headerShown: platform === "ios",
					headerStyle: { backgroundColor: "#bgBase" },
				});
			}
		} finally {
			act(() => renderer.unmount());
		}
		const source = (path: string) => readFileSync(fileURLToPath(new URL(path, import.meta.url)), "utf8");
		expect(source("./ModelPickerSheet.tsx")).toContain('<SheetHeader title={Platform.OS === "ios" ? undefined : "Model"}');
		expect(source("./AgentPickerSheet.tsx")).toContain('<SheetHeader title={Platform.OS === "ios" ? undefined : title}');
		// The agent picker retitles its bar for Chat.
		expect(source("../app/sheets/agent.tsx")).toContain("<Stack.Screen options={{ title }} />");
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
				expect(options.headerShown).not.toBe(false);
			}
			// Titled before the page renames itself to the project.
			expect(renderer.root.findByProps({ name: "project/[id]" }).props.options.title).toBe("Project");
			// Notifications is a sheet on iOS and a page on Android, either way
			// headed by its own stack's bar (app/notifications/_layout).
			const notifications = renderer.root.findByProps({ name: "notifications" }).props.options;
			expect(notifications).toMatchObject({ title: "Notifications", headerShown: false });
			expect(notifications.presentation).toBe(platform === "ios" ? "formSheet" : "card");
			// Pull Requests is presented the same way, headed by app/prs/_layout.
			const prs = renderer.root.findByProps({ name: "prs" }).props.options;
			expect(prs).toMatchObject({ title: "Pull Requests", headerShown: false });
			expect(prs.presentation).toBe(platform === "ios" ? "formSheet" : "card");
		} finally {
			act(() => renderer.unmount());
		}
	});
});
