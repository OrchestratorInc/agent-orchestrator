// Per weight, not from the package root: the barrels pull every face the family
// ships, and this app draws four of the thirty-six.
import { GeistMono_400Regular } from "@expo-google-fonts/geist-mono/400Regular";
import { DarkTheme, DefaultTheme, Stack, ThemeProvider as NavigationThemeProvider } from "expo-router";
import { getFocusedRouteNameFromRoute } from "expo-router/react-navigation";
import * as SplashScreen from "expo-splash-screen";
import { StatusBar } from "expo-status-bar";
import { useFonts } from "expo-font";
import { useEffect, useMemo, useState } from "react";
import { Platform, View } from "react-native";
import { GestureHandlerRootView } from "react-native-gesture-handler";
import { KeyboardProvider } from "react-native-keyboard-controller";
import { SafeAreaProvider } from "react-native-safe-area-context";
import { OnboardingGate } from "../lib/OnboardingGate";
import { TelemetryManager } from "../lib/TelemetryManager";
import { PushManager } from "../lib/PushManager";
import { UpdatesManager } from "../lib/UpdatesManager";
import { StoreUpdateManager } from "../lib/StoreUpdateManager";
import { boardTitle } from "../lib/board-tabs";
import { LayoutGrid } from "../lib/layoutGrid";
import { largeTitleScreenOptions, stackScreenOptions } from "../lib/stackScreenOptions";
import { AppProvider } from "../lib/store";
import { appFonts } from "../lib/appFonts";
import { symbolFonts } from "../lib/symbolFonts";
import { ThemeProvider, useTheme, useThemeState } from "../lib/ThemeProvider";
import { loadThemeSettings, type ThemeSettings } from "../lib/themeStore";
import { sheetCornerRadius } from "../lib/tokens";

// Sheet routes, and how tall each is allowed to be.
//
// A sheet that scrolls needs a real height to scroll within, so it takes
// explicit detents — `fitToContents` cannot measure a scroll view. The usage
// sheet renders a plain view, so it wraps its content exactly.
//
// Input sheets are registered separately below so their heights can respond to
// the keyboard.
//
// `androidDetents` pins an action sheet to one height on Android. With two
// detents Material's BottomSheetBehavior treats the lower one as "collapsed",
// and from there it only hides once the sheet has been dragged past half of
// that height or flung hard; a normal swipe snaps back. With one detent it
// skips the collapsed state and closes on any decisive swipe down, which is
// what an action sheet needs. iOS keeps both detents: UIKit's sheet dismisses
// from the lower one with an ordinary swipe.
const SHEET_ROUTES: readonly { name: string; detents: readonly number[] | "fitToContents"; androidDetents?: readonly [number] }[] = [
	{ name: "sheets/conversation-map", detents: [0.5, 0.95] },
	{ name: "sheets/conversation-usage", detents: "fitToContents" },
	{ name: "sheets/review-actions", detents: [0.6, 0.95], androidDetents: [0.6] },
	{ name: "files/[sessionId]/review", detents: [0.5, 1] },
];

// Picker sheets that iOS titles in a native bar, as it does a form sheet's;
// Android keeps the title in the sheet's body. A page may retitle its bar, as
// the agent picker does for Chat.
const PICKER_SHEETS = [
	{ name: "sheets/agent", title: "Agent" },
	{ name: "sheets/model", title: "Model" },
	{ name: "sheets/chat-settings", title: "Turn settings" },
] as const;

// The manual-connect form — the only sheet with text inputs, and the only one
// that has to move for the keyboard.
//
// Android needs **two** detents to do that, which is not a styling preference:
// react-native-screens only expands a sheet for the IME when it has more than
// one detent. See `SheetDelegate.configureBottomSheetBehaviour`, `KeyboardVisible`
// branch — with 2 or 3 detents it calls `useTwoDetents(STATE_EXPANDED)`, but with
// a single detent (which is what `fitToContents` compiles to) it only attaches a
// callback and leaves the height alone. That is why this sheet sat behind the
// keyboard: a one-detent sheet structurally cannot get out of its way.
//
// So Android opens at the smaller detent and is expanded to the larger one by the
// OS as soon as a field is focused, then restored when the keyboard hides. iOS
// lifts a presented sheet by itself. Its body is a SwiftUI Form, which scrolls
// and fills the sheet rather than measuring to fit, so the sheet opens at a
// fixed height: full for the connection form.
//
// iOS heads these forms with a native bar: an inline title, Cancel and the
// confirm action, as a form sheet's are. Shown from the start rather than by
// the page, because a modal whose header appears after it mounts is rebuilt.
// Android keeps the title in the sheet's body.
const CONNECT_SHEET_OPTIONS = {
	presentation: "formSheet",
	sheetAllowedDetents: Platform.OS === "ios" ? [1] : [0.6, 0.95],
	sheetGrabberVisible: true,
	sheetCornerRadius,
	headerShown: Platform.OS === "ios",
} as const;

// The board sits under every route the app is opened on. A deep link or a
// notification tap that cold-starts the app on a session, a review or a file
// otherwise arrives with nothing behind it, and the system back button only
// appears when there is somewhere to go back to. Opening the app on "/" itself
// is unchanged: the board is then the only route.
export const unstable_settings = { initialRouteName: "(tabs)" };

// Held until the faces are in, so the wait is spent on the splash instead of on
// an empty root. The tree draws nothing without the family, and the splash is the
// only thing that can cover that gap.
void SplashScreen.preventAutoHideAsync().catch(() => {});

export default function RootLayout() {
	// The app's family (DM Sans, after T3 Code), loaded before anything else
	// draws: rendering the tree first would paint one frame of SF Pro and then
	// swap every label under it.
	//
	// `error` matters as much as `loaded`. A face that fails to load never flips
	// `loaded`, so waiting on that flag alone left the app on a blank root forever,
	// with no way out and nothing on screen to say why. A missing family is worth
	// losing to a system fallback; it is not worth losing the app to.
	const [fontsReady, fontError] = useFonts({ ...appFonts, GeistMono_400Regular, ...symbolFonts });
	const fontsSettled = fontsReady || fontError != null;
	// The stored theme is read under the splash too, for the same reason as the
	// fonts: the first frame should already be the user's palette, not the default
	// one swapped out a tick later. The read never rejects (each half falls back
	// to its default), so it cannot hold the splash forever.
	const [themeSettings, setThemeSettings] = useState<ThemeSettings | null>(null);
	useEffect(() => {
		let active = true;
		void loadThemeSettings().then((settings) => {
			if (active) setThemeSettings(settings);
		});
		return () => {
			active = false;
		};
	}, []);
	const ready = fontsSettled && themeSettings !== null;
	useEffect(() => {
		if (ready) void SplashScreen.hideAsync().catch(() => {});
	}, [ready]);
	if (!ready) return null;

	// ThemeProvider sits outside everything that reads a colour, including the
	// Stack's own screenOptions below — hence the inner component: a hook cannot
	// consume a provider its own component renders.
	return (
		<GestureHandlerRootView style={{ flex: 1 }}>
			{/* Sits above everything that positions itself against the keyboard. It
			    reports the IME frame-by-frame, which the platform listeners cannot:
			    Android only fires `keyboardDidShow` once the keyboard has finished
			    animating, so every dock and composer arrived a beat late. */}
			<KeyboardProvider>
				<SafeAreaProvider>
					<ThemeProvider initial={themeSettings}>
						<AppProvider>
							<Shell />
						</AppProvider>
					</ThemeProvider>
				</SafeAreaProvider>
			</KeyboardProvider>
			{/* Dev-only measurement overlay, mounted as a sibling of the whole app so it
			    resolves against the full screen rather than a provider's box. */}
			<LayoutGrid />
		</GestureHandlerRootView>
	);
}

function Shell() {
	const t = useTheme();
	const { scheme } = useThemeState();
	const navigationTheme = useMemo(
		() => ({
			...(scheme === "dark" ? DarkTheme : DefaultTheme),
			colors: {
				...DarkTheme.colors,
				primary: t.accent,
				background: t.bgBase,
				card: t.bgSurface,
				text: t.textPrimary,
				border: t.borderSubtle,
				notification: t.red,
			},
		}),
		[t, scheme],
	);
	return (
		// Themed backdrop behind the navigator. Every view above this one is
		// transparent, so without it the transition between two screens revealed the
		// platform's default view colour — a white flash around the page as it moved.
		<View style={{ flex: 1, backgroundColor: t.bgBase }}>
			{/* Light content on a dark app, dark content on a light one. */}
			<StatusBar style={scheme === "dark" ? "light" : "dark"} />
			<TelemetryManager />
			<PushManager />
			<UpdatesManager />
			<StoreUpdateManager />
			<OnboardingGate />
			{/* The navigator paints surfaces of its own, and it reads them from *its*
			    theme, not from the palette above. Two of those surfaces are visible
			    only while a page moves: the stack's own container, which shows
			    through the seam between the outgoing and incoming screens, and the
			    content view underneath a screen's own background. Left on the
			    navigator's defaults they are white — the edge that appeared around
			    the page mid-transition — so the theme carries our palette into the
			    native layer instead. */}
			<NavigationThemeProvider value={navigationTheme}>
			<Stack screenOptions={stackScreenOptions(t)}>
				{/* A hidden header still needs a title: iOS lists the pages under the
				    current one by title in the back button's long-press menu, and an
				    untitled route shows up there as its file name. The board goes by
				    the tab it is showing. */}
				<Stack.Screen
					name="(tabs)"
					options={({ route }) => ({ headerShown: false, title: boardTitle(getFocusedRouteNameFromRoute(route)) })}
				/>
				{/* Settings holds a stack of its own, whose pages draw the native
				    headers. On iOS it is a full-height sheet that swipes away like any
				    other; a machine's edit page holds it still while it has unsaved
				    changes, and a save or forget in flight holds it too.
				    On Android it is a full-screen page with a top app bar and the
				    system back: a bottom sheet holding a stack of pages is not a
				    Material pattern. */}
				<Stack.Screen
					name="settings"
					options={{
						presentation: Platform.OS === "ios" ? "formSheet" : "card",
						headerShown: false,
						title: "Settings",
						...(Platform.OS === "ios" ? {
							sheetAllowedDetents: [1],
							sheetGrabberVisible: true,
							sheetCornerRadius,
						} : null),
						contentStyle: { backgroundColor: t.bgBase },
					}}
				/>
				<Stack.Screen name="session/[id]" options={{ title: "Session" }} />
				<Stack.Screen name="review/[sessionId]" options={{ title: "Review" }} />
				<Stack.Screen name="reviewer/[reviewId]" options={{ title: "Reviewer" }} />
				<Stack.Screen name="shell/[handleId]" options={{ title: "Worktree shell" }} />
				<Stack.Screen name="preview/[id]" options={{ title: "Preview" }} />
				{/* An agent's page, expanded from the conversation. Modal, so iOS
				    swipes it away; the page titles the bar with its own name. */}
				<Stack.Screen name="render" options={{ presentation: "modal", title: "Page" }} />
				<Stack.Screen name="files/[sessionId]/index" options={{ title: "Files" }} />
				<Stack.Screen name="files/[sessionId]/folder" options={{ title: "Folder" }} />
				<Stack.Screen name="files/[sessionId]/file" options={{ title: "File" }} />
				<Stack.Screen
					name="spawn"
					options={{
						// Android's native form-sheet implementation jumps between
						// detents as soon as the IME appears. A transparent modal lets
						// Spawn render a compact, content-sized sheet and lets RN's
						// KeyboardAvoidingView keep it directly above the keyboard.
						presentation: Platform.OS === "ios" ? "formSheet" : "transparentModal",
						// iOS titles the sheet in a native bar that holds its Cancel.
						// Shown from the start rather than by the page: a modal whose
						// header appears after it mounts is rebuilt, losing the draft.
						// Transparent, so the system draws its own glass over the
						// sheet; the page insets itself by the header's height.
						headerShown: Platform.OS === "ios",
						...(Platform.OS === "ios" ? { headerTransparent: true, headerStyle: undefined } : null),
						title: "New task",
						sheetAllowedDetents: Platform.OS === "ios" ? [0.5, 0.9] : undefined,
						// Opens tall on iOS. At the half detent the keyboard is taller
						// than the sheet, so the selectors and Start task had nowhere to
						// go and ended up clipped beneath it; dragging down to half is
						// still there for anyone who wants the board behind it.
						sheetInitialDetentIndex: Platform.OS === "ios" ? 1 : 0,
						sheetGrabberVisible: Platform.OS === "ios",
						contentStyle: { backgroundColor: Platform.OS === "ios" ? t.bgSurface : "transparent" },
					}}
				/>
				{/* A large title on iOS, like the board it is opened from. */}
				<Stack.Screen name="notifications" options={{ title: "Notifications", ...largeTitleScreenOptions() }} />
				{/* The page renames itself to the project once it loads. */}
				<Stack.Screen name="project/[id]" options={{ title: "Project" }} />
				<Stack.Screen name="onboarding" options={{ headerShown: false, gestureEnabled: false }} />
				{/* iOS bars the modal natively, with its close item; Android keeps a
				    full-screen page with Material's close button in the body. Fixed
				    here, as the page cannot toggle a modal's header without a rebuild. */}
				<Stack.Screen name="pair" options={{ presentation: "modal", headerShown: Platform.OS === "ios", title: "Pair a machine" }} />

				{/* Sheets. `formSheet` is a real UIKit sheet, so the drag-to-dismiss,
				    the grabber and the rubber-banding come from the OS rather than
				    being re-implemented on top of RN's `Modal` (which has no gesture
				    support at all — that's why the hand-rolled version never felt
				    right). Heights come from SHEET_ROUTES. */}
				{SHEET_ROUTES.map(({ name, detents, androidDetents }) => (
					<Stack.Screen
						key={name}
						name={name}
						options={{
							presentation: "formSheet",
							sheetAllowedDetents: Platform.OS === "android" && androidDetents ? [...androidDetents] : detents === "fitToContents" ? "fitToContents" : [...detents],
							sheetInitialDetentIndex: 0,
							sheetGrabberVisible: true,
							sheetCornerRadius,
							headerShown: false,
							contentStyle: { backgroundColor: t.bgSurface },
						}}
					/>
				))}
				{/* Shown from the start rather than by the page, because a modal whose
				    header appears after it mounts is rebuilt. The bar takes the
				    grouped page colour of the iOS list below it. */}
				{PICKER_SHEETS.map(({ name, title }) => (
					<Stack.Screen
						key={name}
						name={name}
						options={{
							presentation: "formSheet",
							sheetAllowedDetents: [0.5, 0.95],
							sheetInitialDetentIndex: 0,
							sheetGrabberVisible: true,
							sheetCornerRadius,
							headerShown: Platform.OS === "ios",
							title,
							headerStyle: { backgroundColor: t.bgBase },
							contentStyle: { backgroundColor: t.bgSurface },
						}}
					/>
				))}
				{/* The composer's skill and file picker searches in a native bar on
				    iOS, under the title the page sets ("Skills", "Worktree files").
				    Android searches in the sheet's body. */}
				<Stack.Screen
					name="sheets/composer-picker"
					options={{
						presentation: "formSheet",
						sheetAllowedDetents: [0.6, 0.95],
						sheetInitialDetentIndex: 0,
						sheetGrabberVisible: true,
						sheetCornerRadius,
						headerShown: Platform.OS === "ios",
						title: "Insert",
						headerStyle: { backgroundColor: t.bgSurface },
						contentStyle: { backgroundColor: t.bgSurface },
					}}
				/>
				<Stack.Screen
					name="sheets/connect"
					options={{
						...CONNECT_SHEET_OPTIONS,
						title: "Connect manually",
						// The bar in the colour of the form below it (the grouped page
						// on iOS, the sheet surface on Android), so the two read as one.
						headerStyle: { backgroundColor: t.bgBase },
						contentStyle: { backgroundColor: Platform.OS === "ios" ? t.bgBase : t.bgSurface },
					}}
				/>
				<Stack.Screen
					name="sheets/machine-name"
					options={Platform.OS === "android" ? {
						presentation: "transparentModal",
						animation: "none",
						headerShown: false,
						contentStyle: { backgroundColor: "transparent" },
					} : {
						...CONNECT_SHEET_OPTIONS,
						title: "Name this machine",
						// Two short rows: half height clears the keyboard on every phone.
						sheetAllowedDetents: [0.5],
						headerStyle: { backgroundColor: t.bgBase },
						contentStyle: { backgroundColor: t.bgBase },
					}}
				/>
			</Stack>
			</NavigationThemeProvider>
		</View>
	);
}
