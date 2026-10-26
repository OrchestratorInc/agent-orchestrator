import { act, create } from "react-test-renderer";
import { afterEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({ platform: { OS: "android" }, theme: new Proxy({}, { get: () => "#000000" }) }));
vi.mock("react-native", () => ({ Platform: fixtures.platform, View: "View" }));
vi.mock("expo-router", async () => {
	const React = await import("react");
	const Stack = Object.assign(({ children, ...props }: { children?: React.ReactNode }) => React.createElement("Stack", props, children), { Screen: "StackScreen" });
	return { Stack, ThemeProvider: "NavigationThemeProvider", DarkTheme: { colors: {} }, DefaultTheme: { colors: {} } };
});
vi.mock("expo-router/react-navigation", () => ({ getFocusedRouteNameFromRoute: () => undefined }));
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

import RootLayout from "../app/_layout";

afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); });

describe("Settings presentation", () => {
	it.each(["android", "ios"])("presents Settings the %s way", async (platform) => {
		fixtures.platform.OS = platform;
		vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
		const error = console.error;
		vi.spyOn(console, "error").mockImplementation((...args) => {
			if (String(args[0]).startsWith("react-test-renderer is deprecated")) return;
			error(...args);
		});
		let renderer!: ReturnType<typeof create>;
		// The root holds its first frame until the stored theme is read.
		await act(async () => { renderer = create(<RootLayout />); });
		try {
			const settings = renderer.root.findByProps({ name: "settings" }).props.options;
			const naming = renderer.root.findByProps({ name: "sheets/machine-name" }).props.options;
			// Settings' own stack draws the headers, never the root's. The title
			// names it in the history of a page opened over it.
			expect(settings.headerShown).toBe(false);
			expect(settings.title).toBe("Settings");
			// Nothing stops a swipe for the whole flow; an open editor holds the
			// sheet still on its own (app/settings/environments.tsx).
			expect(settings.gestureEnabled).toBeUndefined();
			if (platform === "android") {
				// A full-screen page with a top app bar and the system back, not a
				// bottom sheet holding a stack of pages.
				expect(settings.presentation).toBe("card");
				expect(settings.sheetAllowedDetents).toBeUndefined();
				expect(settings.sheetGrabberVisible).toBeUndefined();
				expect(naming.presentation).toBe("transparentModal");
				expect(naming.sheetAllowedDetents).toBeUndefined();
				expect(naming.contentStyle.backgroundColor).toBe("transparent");
			} else {
				// A full-height sheet in the system's own shape, with the grabber
				// that says it swipes away.
				expect(settings.presentation).toBe("formSheet");
				expect(settings.sheetAllowedDetents).toEqual([1]);
				expect(settings.sheetGrabberVisible).toBe(true);
				expect(settings.sheetCornerRadius).toBeUndefined();
				expect(naming.presentation).toBe("formSheet");
				expect(naming.sheetAllowedDetents).toBe("fitToContents");
			}
		} finally {
			act(() => renderer.unmount());
		}
	});
});
