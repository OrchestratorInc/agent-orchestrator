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
		// The prompt is the chat composer's own field, in the shared shell.
		expect(spawn).not.toContain("SpawnPromptInput");
		expect(source("./spawn-composer.tsx")).toContain("<TextInput");
		// A page on both platforms, so nothing needs a sheet of its own.
		expect(spawn).not.toContain("@expo/ui/community/bottom-sheet");
		expect(source("../app/_layout.tsx")).toContain('<Stack.Screen name="spawn" options={{ title: "New task" }} />');
	});

	// Spawn's choices open on its own page. They used to be a second sheet over
	// Spawn's: two grabbers, and only the top one answered a swipe down. Each is
	// now a Material dropdown anchored to its own button, rather than an inline
	// list that replaced the controls.
	it("shows Spawn's choices inside Spawn's own sheet", () => {
		const android = source("./spawn-composer-controls.android.tsx");
		expect(android).toContain('from "@expo/ui/jetpack-compose"');
		expect(android).toContain("<DropdownMenu expanded={expanded} onDismissRequest={onDismiss}");
		expect(android).not.toContain("OptionList");
		expect(android).not.toMatch(/\bModal\b/);
		expect(android).not.toContain("@expo/ui/community/bottom-sheet");
		expect(android).not.toContain("Picker");
		expect(android).not.toContain("Pressable");
		expect(android).not.toContain("Feather");
		expect(android).toContain("option.sectionBreakBefore ? <HorizontalDivider");
		expect(android).toContain('project?.icon === "message-square-plus" ? materialDrawable("add_comment") : materialDrawable("folder")');
		expect(android).toContain("chipColorFor(harness)");
		// The approval mode sits in the composer's line, after the agent.
		expect(android.indexOf('testID("spawn-approval")')).toBeGreaterThan(android.indexOf("settings={"));
		expect(android.indexOf('testID("spawn-approval")')).toBeGreaterThan(android.indexOf('testID("spawn-harness")'));
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
	it("filters and spawns Android workers with native controls", () => {
		const board = source("../app/(tabs)/(workers)/index.tsx");
		expect(board).toContain('<AddFab expanded={fab.expanded} label="Spawn worker" onPress={spawnWorker} bottomInset={tabBarInset + tabBar.inset} testID="spawn-worker" />');
		expect(source("./BoardHeaderItems.tsx")).toContain("{!ios ? filter : null}");
		expect(source("./AddFab.android.tsx")).toContain('import { ExtendedFloatingActionButton, Host, Icon, Text } from "@expo/ui/jetpack-compose";');
		for (const gone of ["./worker-dock.android.tsx", "./worker-controls-sheet.android.tsx", "./worker-controls-sheet.tsx"]) {
			expect(existsSync(fileURLToPath(new URL(gone, import.meta.url)))).toBe(false);
		}
	});

	// The new task's composer is the chat's on both platforms: its pill, its
	// field, its attach menu and its round button.
	it("draws the Spawn composer as the chat composer", () => {
		const shell = source("./spawn-composer.tsx");
		expect(shell).not.toContain("@expo/ui");
		expect(shell).toContain("<ChatAttachmentMenu");
		expect(shell).toContain("<ComposerGlass radius={COMPOSER_RADIUS} />");
		expect(shell).toContain("...composerStyleRules(t),");
		expect(shell).toContain('style={[styles.controlsRow, { paddingLeft: CORNER_CONTROL, paddingRight: CORNER_CONTROL }, controlsOpening]}');
	});

	// The turn's settings have no sheet of their own: they are the controls
	// row's dropdowns, opened from React Native buttons that are real
	// accessibility nodes, as the attach button's is.
	it("picks Android turn settings from the controls row's own menus", () => {
		expect(existsSync(fileURLToPath(new URL("./chat/ChatSettingsModal.tsx", import.meta.url)))).toBe(false);
		const control = source("./chat/ChatTurnSettingsControl.tsx");
		expect(control).toContain("<MenuView ref={menu} colorScheme={scheme} actions={actions} onPressAction={onAction}");
		expect(control).toContain("onPress={() => menu.current?.show()}");
		expect(control).not.toContain("All turn settings");
	});

	it("dismisses the Android keyboard before presenting chat sheets", () => {
		const screen = source("./chat/ChatSessionScreen.tsx");
		expect(screen).toContain("dismissKeyboardBeforeSheet");
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
		// A page on both platforms: no presentation of its own.
		expect(rootLayout).toContain('<Stack.Screen name="settings" options={{ headerShown: false, title: "Settings", contentStyle: { backgroundColor: t.bgBase } }} />');
	});

	// The conversation's actions are a page of Material list rows, each led by
	// a bundled drawable, opened from the top app bar's "⋯".
	it("opens conversation actions as a Material grouped list page", () => {
		const page = source("./chat/ConversationActionsPage.android.tsx");
		expect(page).toContain("<GroupedSection key={group.title} title={group.title}>");
		expect(page).toContain("source={materialDrawable(item.icon.android)}");
		expect(source("./chat/ChatSessionScreen.tsx")).toContain('<Stack.Toolbar.Button icon={toolbarIcon("more")} accessibilityLabel="Conversation actions" onPress={openActions} />');
		expect(source("./toolbarIcon.android.ts")).toContain('more_vert: require("../assets/icons/more_vert.xml")');
	});
});
