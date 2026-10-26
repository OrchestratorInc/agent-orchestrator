import { Stack } from "expo-router";
import type { ReactElement } from "react";
import { Platform } from "react-native";
import { haptics } from "../haptics";
import { toolbarIcon } from "../toolbarIcon";

/** What Preview shows: the desktop's own browser, streamed, or the agent's app rendered on the phone. */
export type PreviewMode = "browser" | "app";

export type PreviewModeMenuOptions = {
	mode: PreviewMode;
	/** Whether the session has an app preview to show on the phone yet. */
	appAvailable: boolean;
	onSelect(mode: PreviewMode): void;
};

/**
 * Preview's two views as one native header menu, each with the system's check
 * on the one showing: a UIMenu on iOS, Compose's DropdownMenu on Android. The
 * glyph is the view's own device, so the bar says which one is up without
 * relying on colour.
 *
 * A function rather than a component: Stack.Toolbar reads its children by
 * element type. Memoise the result; every new element registers the header's
 * options again.
 */
export function previewModeMenu({ mode, appAvailable, onSelect }: PreviewModeMenuOptions): ReactElement {
	const ios = Platform.OS === "ios";
	// Mobile view stays choosable while it is the one up, even as its preview reloads.
	const appUnavailable = !appAvailable && mode !== "app";
	const choose = (next: PreviewMode) => {
		if (next === mode) return;
		haptics.select();
		onSelect(next);
	};
	return (
		<Stack.Toolbar.Menu
			icon={toolbarIcon(mode === "app" ? { ios: "iphone", android: "smartphone" } : "desktop")}
			// Android's menu items draw the check without a selected state, so the
			// current view is in the menu's own label for TalkBack.
			accessibilityLabel={mode === "app" ? "View, Mobile view" : "View, Desktop browser"}
			accessibilityHint="Switches between the desktop browser and the app preview on this phone."
		>
			<Stack.Toolbar.MenuAction
				// Android menus here take only bundled drawables; the check marks the choice there.
				icon={ios ? "desktopcomputer" : undefined}
				isOn={mode === "browser"}
				onPress={() => choose("browser")}
			>
				Desktop browser
			</Stack.Toolbar.MenuAction>
			<Stack.Toolbar.MenuAction
				icon={ios ? "iphone" : undefined}
				isOn={mode === "app"}
				disabled={appUnavailable}
				// Android's menu draws neither subtitles nor hints, so why the item
				// is greyed out goes in its own text there.
				subtitle={appUnavailable && ios ? "No app preview for this session yet" : undefined}
				onPress={() => choose("app")}
			>
				{appUnavailable && !ios ? "Mobile view (no app preview yet)" : "Mobile view"}
			</Stack.Toolbar.MenuAction>
		</Stack.Toolbar.Menu>
	);
}
