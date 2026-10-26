import { existsSync, readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

function source(relativePath: string): string {
	return readFileSync(fileURLToPath(new URL(relativePath, import.meta.url)), "utf8");
}

describe("Android native compatibility boundaries", () => {
	// Material's own icon button: the 48dp target, the ripple and the click all
	// come from Compose, so no touch is recognised by hand in JavaScript.
	it("draws the header button as a Compose icon button with a Material Symbol", () => {
		const headerButton = source("./native-header-button.android.tsx");
		expect(headerButton).toContain('from "@expo/ui/jetpack-compose"');
		expect(headerButton).toMatch(/<IconButton onClick=\{onPress\}/);
		expect(headerButton).toContain("materialDrawable(nativeHeaderGlyphs[icon].android)");
		expect(headerButton).toContain("<BadgedBox>");
		for (const handRolled of ["Pressable", "onTouchStart", "onTouchEnd", "setTimeout", "Feather"]) {
			expect(headerButton).not.toContain(handRolled);
		}
	});

	it("keeps SwiftUI and percentage native widths out of the cross-platform Spawn route", () => {
		const spawn = source("../app/spawn.tsx");
		expect(spawn).not.toContain("@expo/ui/swift-ui");
		expect(spawn).not.toContain("NativeTextInput");
		expect(spawn).toContain("SpawnPromptInput");

		const path = fileURLToPath(new URL("./spawn-prompt-input.android.tsx", import.meta.url));
		expect(existsSync(path)).toBe(true);
		const android = existsSync(path) ? source("./spawn-prompt-input.android.tsx") : "";
		expect(android).toContain("TextInput");
		expect(android).not.toContain("autoFocus");
		expect(android).not.toContain('width: "100%"');
		expect(spawn).toContain('@expo/ui/community/bottom-sheet');
		expect(spawn).toContain("BottomSheetView");
		expect(spawn).toContain("enablePanDownToClose");
		expect(spawn).not.toContain("KeyboardAvoidingView");
		// The controls stick to the keyboard instead, which is the only approach
		// that moves in step with it inside a native form sheet.
		expect(spawn).toContain("KeyboardStickyView");
		expect(spawn).not.toContain("androidGrabber");
		// Only iOS's prompt flexes to fill the sheet; Android's sheet sizes to its
		// content, so a flexing child there would have nothing to fill.
		expect(spawn).toContain('Platform.OS === "ios" && styles.promptHostFill');
		expect(spawn).toContain("const PROMPT_MIN_HEIGHT = 112;");
		expect(spawn).toContain('promptHost: { width: "100%", height: PROMPT_MIN_HEIGHT }');
		expect(source("../app/_layout.tsx")).toContain('presentation: Platform.OS === "ios" ? "formSheet" : "transparentModal"');
	});

	// Spawn is itself a bottom sheet on Android, so its choices open inside it.
	// They used to be a second sheet over the first: two grabbers, and only the
	// top one answered a swipe down.
	it("shows Spawn's choices inside Spawn's own sheet", () => {
		const android = source("./spawn-composer-controls.android.tsx");
		const icons = source("./icons.tsx");
		expect(android).toContain("OptionList");
		expect(android).not.toMatch(/\bModal\b/);
		expect(android).not.toContain("@expo/ui/community/bottom-sheet");
		expect(android).not.toContain("Picker");
		expect(android).not.toContain("@expo/ui");
		expect(android).toContain("AgentLogo");
		expect(android).toContain("option.sectionBreakBefore && styles.optionSectionBreak");
		expect(android).toContain('icon={selectedProject?.icon ?? "folder"}');
		expect(android).toContain('option.icon ? <Feather name={option.icon}');
		expect(icons).toContain('import MessageSquarePlus from "lucide-react-native/icons/message-square-plus";');
		expect(icons).toContain('"message-square-plus": MessageSquarePlus');
	});

	it("uses a rounded native Android attachment chooser instead of the square popup menu", () => {
		const path = fileURLToPath(new URL("./chat/ChatAttachmentMenu.android.tsx", import.meta.url));
		expect(existsSync(path)).toBe(true);
		const android = existsSync(path) ? source("./chat/ChatAttachmentMenu.android.tsx") : "";
		expect(android).toContain('@expo/ui/community/bottom-sheet');
		expect(android).toContain("enablePanDownToClose");
		expect(android).toContain("borderRadius: radius.pill");
		expect(android).not.toContain("MenuView");
	});

	it("uses React Native Android inputs for elicitation", () => {
		const file = "./chat/elicitation-native-controls.android.tsx";
		const path = fileURLToPath(new URL(file, import.meta.url));
		expect(existsSync(path)).toBe(true);
		const android = existsSync(path) ? source(file) : "";
		expect(android).toContain("TextInput");
		expect(android).not.toContain("@expo/ui");
		expect(android).not.toContain('width: "100%"');
	});

	// The top app bar's own search action and a Compose dropdown take the place
	// of the hand-built dock and its bottom sheet, and spawning is Material's FAB.
	it("searches, filters and spawns Android workers with native controls", () => {
		const board = source("../app/(tabs)/(workers)/index.tsx");
		expect(board).toContain("<Stack.SearchBar");
		expect(board).toContain("onClose={searchClosed}");
		expect(board).toContain('<AddFab label="Spawn worker" onPress={spawnWorker} bottomInset={tabBarInset} testID="spawn-worker" />');
		expect(source("./BoardHeaderItems.tsx")).toContain("{!ios ? filter : null}");
		expect(source("./AddFab.android.tsx")).toContain('import { FloatingActionButton, Host, Icon } from "@expo/ui/jetpack-compose";');
		for (const gone of ["./worker-dock.android.tsx", "./worker-controls-sheet.android.tsx", "./worker-controls-sheet.tsx"]) {
			expect(existsSync(fileURLToPath(new URL(gone, import.meta.url)))).toBe(false);
		}
	});

	it("makes the entire iOS Spawn prompt a native text-input hit target", () => {
		const ios = source("./spawn-prompt-input.ios.tsx");
		// A SwiftUI TextField keeps an intrinsic one-line hit target even when its
		// Host is tall. React Native's native TextInput owns the full frame, so every
		// visible point in the prompt area focuses the editor.
		expect(ios).toContain('import { StyleSheet, TextInput } from "react-native"');
		expect(ios).not.toContain('@expo/ui');
		expect(ios).toContain("height = 112");
		expect(ios).toMatch(/paddingHorizontal:\s*space\.lg/);
		expect(ios).toMatch(/paddingTop:\s*space\.huge/);
		expect(ios).toMatch(/paddingBottom:\s*space\.md/);
		expect(ios).toContain('textAlignVertical="top"');
		expect(ios).toContain("scrollEnabled");
		expect(ios).toMatch(/style=\{\[styles\.input,\s*\{\s*height,/);
	});

	// The native bar now heads the iOS sheet, so the prompt starts at the
	// content's own inset below it rather than leaving room for the grabber.
	it("starts the iOS Spawn prompt at the content inset below the native bar", () => {
		const spawn = source("../app/spawn.tsx");
		expect(spawn).not.toContain("iosContent");
		expect(spawn).toMatch(/content:\s*\{[^}]*paddingTop:\s*space\.lg/);
	});

	it("uses compact app-native choice rows instead of the unstable Compose picker", () => {
		const path = fileURLToPath(new URL("./chat/ChatSettingsModal.android.tsx", import.meta.url));
		expect(existsSync(path)).toBe(true);
		const android = existsSync(path) ? source("./chat/ChatSettingsModal.android.tsx") : "";
		expect(android).toContain("ChoicePage");
		expect(android).toContain("SettingRow");
		expect(android).not.toContain("Picker");
		// The turn-settings route is already a native form sheet, so a choice list
		// opens as a page within it rather than a sheet on top.
		expect(android).not.toContain('@expo/ui/community/bottom-sheet');
		expect(android).not.toMatch(/\bModal\b/);
		expect(android).toContain('numberOfLines={1}');
		expect(android).not.toContain('description ? <Text numberOfLines={1} style={styles.rowDescription}');
		expect(android).toContain('<View style={styles.choiceCopy}>');
		expect(android).toMatch(/choiceCopy:\s*\{\s*flex:\s*1,\s*minWidth:\s*0/);
		expect(android).toMatch(/choiceRow:\s*\{[^}]*alignItems:\s*"flex-start"/s);
		expect(android).not.toContain('width: "100%"');
	});

	it("dismisses the Android keyboard before presenting chat sheets", () => {
		const screen = source("./chat/ChatSessionScreen.tsx");
		expect(screen).toContain("dismissKeyboardBeforeSheet");
		expect(screen).toMatch(/openTurnSettings[\s\S]*await dismissKeyboardBeforeSheet/);
		expect(screen).toMatch(/menuOpen[\s\S]*dismissKeyboardBeforeSheet/);
	});

	it("pins conversation actions to one Android detent on first open", () => {
		const layout = source("../app/_layout.tsx");
		expect(layout).toContain('{ name: "sheets/conversation-actions", detents: [0.6, 0.95], androidDetents: [0.6] }');
		expect(layout).toContain('Platform.OS === "android" && androidDetents ? [...androidDetents]');
	});

	// With two detents Material only hid the sheet after a drag past half its
	// height, so a normal swipe down snapped back and closing felt random.
	it("pins Android review actions to one 60% detent so a swipe down closes it", () => {
		const layout = source("../app/_layout.tsx");
		const actions = source("../app/sheets/review-actions.tsx");
		expect(layout).toContain('{ name: "sheets/review-actions", detents: [0.6, 0.95], androidDetents: [0.6] }');
		expect(layout).toContain("sheetInitialDetentIndex: 0");
		expect(layout).toContain("sheetGrabberVisible: true");
		expect(actions).toMatch(/<ScrollView[^>]*nestedScrollEnabled/);
	});

	it("does not register the built-in Android sound as a missing custom asset", () => {
		expect(source("./push.ts")).not.toContain('sound: "default"');
	});

	// A full-screen page over the board on Android, with a top app bar and the
	// system back; a sheet on iOS.
	it("presents Settings from the root stack over the board", () => {
		const rootSettingsPath = fileURLToPath(new URL("../app/settings/index.tsx", import.meta.url));
		const nestedSettingsPath = fileURLToPath(new URL("../app/(tabs)/settings.tsx", import.meta.url));
		expect(existsSync(rootSettingsPath)).toBe(true);
		expect(existsSync(nestedSettingsPath)).toBe(false);
		const rootLayout = source("../app/_layout.tsx");
		expect(rootLayout).toMatch(/name="settings"\s*options=\{\{\s*presentation: Platform\.OS === "ios" \? "formSheet" : "card",/);
	});

	it("renders conversation actions in a single native list with its conversation header", () => {
		const actions = source("./chat/ConversationActionsSheet.tsx");
		const registry = source("./chat/chatSheetRegistry.ts");
		expect(actions).toContain("<FlatList");
		expect(actions).toMatch(/ListHeaderComponent=\{<SheetHeader[\s\S]*?\/>}/);
		expect(actions).not.toContain("<ScrollView");
		expect(registry).toContain("sessionTitle: string");
		expect(actions).toContain('title={snapshot.title || "Untitled conversation"}');
		expect(actions).toContain('subtitle={`Session · ${entry.sessionTitle}`}');
		expect(actions).toContain("backgroundColor: t.bgBase");
		expect(actions).toContain("backgroundColor: t.bgElevated");
		expect(actions).not.toMatch(/<ScrollView[\s\S]*<View style=\{styles\.header\}>/);
	});

	it("keeps Android conversation-menu drags with the list instead of dismissing its sheet", () => {
		const actions = source("./chat/ConversationActionsSheet.tsx");
		expect(actions).toMatch(/<FlatList[\s\S]*nestedScrollEnabled/);
	});
});
