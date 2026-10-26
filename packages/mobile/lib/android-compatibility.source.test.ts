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
	// top one answered a swipe down. Each is now a Material dropdown anchored to
	// its own button, rather than an inline list that replaced the controls.
	it("shows Spawn's choices inside Spawn's own sheet", () => {
		const android = source("./spawn-composer-controls.android.tsx");
		const icons = source("./icons.tsx");
		expect(android).toContain('from "@expo/ui/jetpack-compose"');
		expect(android).toContain("<DropdownMenu expanded={expanded} onDismissRequest={onDismiss}");
		expect(android).not.toContain("OptionList");
		expect(android).not.toMatch(/\bModal\b/);
		expect(android).not.toContain("@expo/ui/community/bottom-sheet");
		expect(android).not.toContain("Picker");
		expect(android).not.toContain("Pressable");
		expect(android).not.toContain("Feather");
		expect(android).toContain("option.sectionBreakBefore ? <HorizontalDivider");
		expect(android).toContain('project?.icon === "message-square-plus" ? spawnDrawables.standalone : materialDrawable("folder")');
		expect(android).toContain("chipColorFor(harness)");
		expect(icons).toContain('import MessageSquarePlus from "lucide-react-native/icons/message-square-plus";');
		expect(icons).toContain('"message-square-plus": MessageSquarePlus');
	});

	// The attachment chooser is the same pull-down menu on both platforms, so
	// Android gets Compose's dropdown with Material Symbol icons, not a sheet.
	it("attaches through the shared native menu instead of an Android sheet", () => {
		expect(existsSync(fileURLToPath(new URL("./chat/ChatAttachmentMenu.android.tsx", import.meta.url)))).toBe(false);
		const menu = source("./chat/ChatAttachmentMenu.tsx");
		expect(menu).toContain("<MenuView ref={menu}");
		expect(menu).toContain('image: chatMenuIcon("photo")');
		expect(menu).not.toContain("bottom-sheet");
	});

	// Android draws the shared file's Material 3 controls: real radios and
	// checkboxes with their row semantics, an outlined field and Compose buttons,
	// instead of glyph-drawn imitations.
	it("answers elicitation with Compose controls", () => {
		expect(existsSync(fileURLToPath(new URL("./chat/elicitation-native-controls.android.tsx", import.meta.url)))).toBe(false);
		const controls = source("./chat/elicitation-native-controls.tsx");
		expect(controls).toContain('from "@expo/ui/jetpack-compose"');
		expect(controls).toContain('selectable(checked, choose, "radioButton")');
		expect(controls).toContain('toggleable(checked, choose, { role: "checkbox" })');
		expect(controls).toContain("<RadioButton selected={checked} />");
		expect(controls).toContain("<Checkbox value={checked} />");
		expect(controls).toContain("<OutlinedTextField");
		for (const imitation of ["Feather", "Pressable", "rgba(", 'width: "100%"']) {
			expect(controls).not.toContain(imitation);
		}
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

	// One turn-settings sheet serves both platforms. On Android its choices open
	// in Compose's dropdown, not the exposed-dropdown picker, and inside the
	// sheet rather than as a second sheet on top; its Compose rows sit in a
	// React Native scroll view, which Material's sheet hands a drag to.
	it("picks Android turn settings from Compose menus inside the sheet", () => {
		expect(existsSync(fileURLToPath(new URL("./chat/ChatSettingsModal.android.tsx", import.meta.url)))).toBe(false);
		const sheet = source("./chat/ChatSettingsModal.tsx");
		expect(sheet).toContain("<DropdownMenu expanded={expanded} onDismissRequest={() => setExpanded(false)}>");
		expect(sheet).not.toContain("ExposedDropdownMenuBox");
		expect(sheet).not.toContain('@expo/ui/community/bottom-sheet');
		expect(sheet).not.toMatch(/\bModal\b/);
		expect(sheet).toContain("<ScrollView\n\t\t\t\tnestedScrollEnabled");
		expect(sheet).not.toContain('width: "100%"');
	});

	it("dismisses the Android keyboard before presenting chat sheets", () => {
		const screen = source("./chat/ChatSessionScreen.tsx");
		expect(screen).toContain("dismissKeyboardBeforeSheet");
		expect(screen).toMatch(/openTurnSettings[\s\S]*await dismissKeyboardBeforeSheet/);
		expect(screen).toMatch(/const presentSheet = useCallback\(async \(entry: ChatSheetEntry\) => \{\s*await dismissKeyboardBeforeSheet/);
	});

	// The usage details are a few short rows, so the sheet wraps them: one
	// Android detent, which a swipe down closes.
	it("sizes the conversation usage sheet to its rows", () => {
		const layout = source("../app/_layout.tsx");
		expect(layout).toContain('{ name: "sheets/conversation-usage", detents: "fitToContents" }');
		expect(layout).not.toContain("sheets/conversation-actions");
	});

	// With two detents Material only hid the sheet after a drag past half its
	// height, so a normal swipe down snapped back and closing felt random.
	it("pins Android review actions to one 60% detent so a swipe down closes it", () => {
		const layout = source("../app/_layout.tsx");
		const actions = source("./review-actions-sheet.tsx");
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

	// Material's overflow menu from the top app bar, as iOS gets UIKit's pull-down:
	// the root needs a bundled drawable on Android, and the sections are inline
	// submenus, which Compose draws as dividers.
	it("opens conversation actions as the top app bar's overflow menu", () => {
		const menu = source("./chat/conversationActionsMenu.tsx");
		expect(menu).toContain('<Stack.Toolbar.Menu icon={toolbarIcon("more")} accessibilityLabel="Conversation actions">');
		expect(menu).toContain("<Stack.Toolbar.Menu key={group.title} inline>");
		expect(menu).toContain('icon={Platform.OS === "ios" ? item.icon : undefined}');
		expect(source("./toolbarIcon.android.ts")).toContain('more_vert: require("../assets/icons/more_vert.xml")');
	});
});
