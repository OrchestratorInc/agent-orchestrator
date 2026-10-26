import { Stack } from "expo-router";
import type { ReactElement } from "react";
import { Platform, type ColorValue } from "react-native";
import type { BrowserLiveState } from "../browserLive";
import { toolbarIcon } from "../toolbarIcon";

/**
 * Whether the controls go in a bottom toolbar: Material 3's floating toolbar,
 * which Android draws inside the page and takes down with it.
 *
 * iOS takes them from one header menu instead. expo-router's iOS bottom
 * toolbar is the navigation controller's UIToolbar: it fills it only from iOS
 * 18, and once shown never hides it again, so it would stay, empty, over the
 * board and its tab bar after leaving Preview.
 */
export function hasBottomToolbar(): boolean {
	return Platform.OS === "android";
}

/** How much of the screen's foot the floating toolbar covers above the safe area. */
export function bottomToolbarHeight(): number {
	return hasBottomToolbar() ? 64 : 0;
}

export type BrowserTab = BrowserLiveState["tabs"][number];

export type BrowserControls = {
	/** The stream is open, so commands reach the desktop. */
	enabled: boolean;
	canGoBack: boolean;
	canGoForward: boolean;
	loading: boolean;
	tabs: readonly BrowserTab[];
	/** Glyph colour, so the bar matches the header's items. */
	tint: ColorValue;
	/**
	 * A disabled glyph's colour on Android, where the toolbar's icon buttons
	 * keep their tint when disabled. iOS dims a disabled bar item itself.
	 */
	disabledTint: ColorValue;
	/** Android's toolbar and menu surface, in place of Material You's. */
	surface: ColorValue;
	onBack(): void;
	onForward(): void;
	onReload(): void;
	onStop(): void;
	onOpenURL(): void;
	onNewTab(): void;
	onSelectTab(tabId: string): void;
	onCloseTab(tabId: string): void;
};

const forwardSymbol = { ios: "chevron.forward", android: "arrow_forward" } as const;
const tabsSymbol = { ios: "square.on.square", android: "tab" } as const;

/** What a tab is called in the tab list: its page's title, else its address. */
export function browserTabLabel(tab: BrowserTab): string {
	return tab.title.trim() || tab.url || "New tab";
}

/**
 * The live browser's controls as the screen's bottom toolbar: Back, Forward,
 * Reload (Stop while a page loads) and the tab menu, in Material 3's floating
 * toolbar.
 *
 * A function rather than a component: Stack.Toolbar reads its children by
 * element type.
 */
export function browserToolbar(controls: BrowserControls): ReactElement {
	const { enabled, canGoBack, canGoForward, loading } = controls;
	// Compose's icon buttons keep an icon's tint when disabled, so a disabled
	// item is given the dimmed one.
	const tint = (disabled: boolean) => disabled ? controls.disabledTint : controls.tint;
	return (
		<Stack.Toolbar tintColor={controls.tint} backgroundColor={controls.surface}>
			<Stack.Toolbar.Button icon={toolbarIcon("back")} accessibilityLabel="Back" tintColor={tint(!enabled || !canGoBack)} disabled={!enabled || !canGoBack} onPress={controls.onBack} />
			<Stack.Toolbar.Button icon={toolbarIcon(forwardSymbol)} accessibilityLabel="Forward" tintColor={tint(!enabled || !canGoForward)} disabled={!enabled || !canGoForward} onPress={controls.onForward} />
			<Stack.Toolbar.Spacer />
			{/* One item that changes in place, as Safari's does, rather than two
			    trading places while a page loads. */}
			<Stack.Toolbar.Button
				icon={toolbarIcon(loading ? "close" : "refresh")}
				accessibilityLabel={loading ? "Stop loading" : "Reload"}
				tintColor={tint(!enabled)}
				disabled={!enabled}
				onPress={loading ? controls.onStop : controls.onReload}
			/>
			<Stack.Toolbar.Spacer />
			<Stack.Toolbar.Menu
				icon={toolbarIcon(tabsSymbol)}
				accessibilityLabel={controls.tabs.length === 1 ? "Tabs, 1 open" : `Tabs, ${controls.tabs.length} open`}
				tintColor={tint(!enabled)}
				disabled={!enabled}
			>
				{tabActions(controls)}
				{tabList(controls)}
			</Stack.Toolbar.Menu>
		</Stack.Toolbar>
	);
}

/**
 * The same controls as one header menu, for iOS: a UIMenu that leads with
 * Back, Forward and Reload as a row of icons (a palette, from iOS 17; a short
 * list before it), then the tab actions and the open tabs. Back stays inside
 * it rather than in the bar, where it would sit across from the system's own.
 */
export function browserMenu(controls: BrowserControls): ReactElement {
	const { enabled, canGoBack, canGoForward, loading } = controls;
	return (
		<Stack.Toolbar.Menu icon={toolbarIcon("more")} accessibilityLabel="Browser controls" disabled={!enabled}>
			<Stack.Toolbar.Menu inline palette>
				<Stack.Toolbar.MenuAction icon="chevron.backward" disabled={!enabled || !canGoBack} onPress={controls.onBack}>Back</Stack.Toolbar.MenuAction>
				<Stack.Toolbar.MenuAction icon="chevron.forward" disabled={!enabled || !canGoForward} onPress={controls.onForward}>Forward</Stack.Toolbar.MenuAction>
				<Stack.Toolbar.MenuAction icon={loading ? "xmark" : "arrow.clockwise"} disabled={!enabled} onPress={loading ? controls.onStop : controls.onReload}>
					{loading ? "Stop loading" : "Reload"}
				</Stack.Toolbar.MenuAction>
			</Stack.Toolbar.Menu>
			{tabActions(controls)}
			{tabList(controls)}
		</Stack.Toolbar.Menu>
	);
}

/**
 * Open URL, New tab and Close this tab. Listed loose rather than as a section:
 * Android draws an inline section as a divider, which would lead the menu.
 */
function tabActions(controls: BrowserControls): ReactElement[] {
	const ios = Platform.OS === "ios";
	const disabled = !controls.enabled;
	const active = controls.tabs.find((tab) => tab.active);
	const actions = [
		// Android menus here take only bundled drawables, so their actions are text.
		<Stack.Toolbar.MenuAction key="open" icon={ios ? "link" : undefined} disabled={disabled} onPress={controls.onOpenURL}>Open URL…</Stack.Toolbar.MenuAction>,
		<Stack.Toolbar.MenuAction key="new" icon={ios ? "plus.square.on.square" : undefined} disabled={disabled} onPress={controls.onNewTab}>New tab</Stack.Toolbar.MenuAction>,
	];
	if (active && controls.tabs.length > 1) {
		actions.push(
			<Stack.Toolbar.MenuAction key="close" icon={ios ? "xmark" : undefined} destructive disabled={disabled} onPress={() => controls.onCloseTab(active.id)}>
				Close this tab
			</Stack.Toolbar.MenuAction>,
		);
	}
	return actions;
}

/** The open tabs, the showing one checked; choosing another brings it forward on the desktop. */
function tabList(controls: BrowserControls): ReactElement | null {
	if (controls.tabs.length === 0) return null;
	return (
		<Stack.Toolbar.Menu inline title="Open tabs">
			{controls.tabs.map((tab) => (
				<Stack.Toolbar.MenuAction
					key={tab.id}
					isOn={tab.active}
					disabled={!controls.enabled}
					subtitle={tab.title.trim() && tab.url ? tab.url : undefined}
					onPress={() => { if (!tab.active) controls.onSelectTab(tab.id); }}
				>
					{browserTabLabel(tab)}
				</Stack.Toolbar.MenuAction>
			))}
		</Stack.Toolbar.Menu>
	);
}
