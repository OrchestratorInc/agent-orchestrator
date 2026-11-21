import { Stack } from "expo-router";
import { useMemo, type ReactElement } from "react";
import { Platform } from "react-native";
import { useSidebarMaybe } from "./sidebar/sidebarContext";
import { useTheme } from "./ThemeProvider";
import { toolbarIcon } from "./toolbarIcon";

/** What the badge prints; past this it stops growing. */
const MAX_BADGE = 99;

// The desktop's sidebar toggle is lucide's PanelLeft; iOS's own drawing of the
// same idea is SF Symbols' sidebar.left. Android's top app bar opens a drawer
// from the Material menu glyph, as every Android app does.
export const SIDEBAR_ICON = toolbarIcon({ ios: "sidebar.left", android: "menu" });
// New task, in the compose glyph Notes and Mail use (desktop: SquarePen).
const NEW_TASK_ICON = toolbarIcon({ ios: "square.and.pencil", android: "edit_square" });

export type BoardHeaderItemsProps = {
	/**
	 * Sessions waiting on you plus unread notifications: the sidebar button
	 * carries the count, as the desktop's sidebar shows them on its rows.
	 */
	badge: number;
	/**
	 * The page's filter: a `Stack.Toolbar.Menu` element, memoised by the page.
	 * iOS puts it beside the sidebar button; Android's top app bar has no
	 * leading actions beyond the drawer button, so it heads the trailing ones.
	 */
	filter?: ReactElement | null;
	/** New task, ending the bar. Absent when there is nowhere to start one. */
	onNewTask?: () => void;
};

/**
 * The board's header items: the sidebar button that opens the desktop's left
 * sidebar as a drawer, the board's filter, and New task. Settings, Pull
 * Requests and Notifications are no longer separate bar buttons: they live in
 * the sidebar, where the desktop keeps them.
 *
 * Memoised on what it shows: the board re-renders on every poll, and each new
 * toolbar element would register the header's options again.
 */
export function BoardHeaderItems({ badge, filter = null, onNewTask }: BoardHeaderItemsProps) {
	const t = useTheme();
	const sidebar = useSidebarMaybe();
	const open = sidebar?.open;
	const ios = Platform.OS === "ios";
	const ink = ios ? t.textPrimary : undefined;
	const leading = useMemo(() => (
		<Stack.Toolbar placement="left">
			<Stack.Toolbar.Button
				icon={SIDEBAR_ICON}
				tintColor={ink}
				hidesSharedBackground
				// iOS draws bar-item badges only from iOS 26, so the count is in the label too.
				accessibilityLabel={badge > 0 ? `Open sidebar, ${badge} need${badge === 1 ? "s" : ""} you` : "Open sidebar"}
				onPress={() => open?.()}
			>
				{badge > 0 ? <Stack.Toolbar.Badge>{badge > MAX_BADGE ? `${MAX_BADGE}+` : String(badge)}</Stack.Toolbar.Badge> : null}
			</Stack.Toolbar.Button>
			{ios ? filter : null}
		</Stack.Toolbar>
	), [badge, filter, ink, ios, open]);
	const trailing = useMemo(() => (
		<Stack.Toolbar placement="right">
			{!ios ? filter : null}
			{onNewTask ? <Stack.Toolbar.Button icon={NEW_TASK_ICON} tintColor={ink} hidesSharedBackground accessibilityLabel="New task" onPress={onNewTask} /> : null}
		</Stack.Toolbar>
	), [filter, ink, ios, onNewTask]);
	return (
		<>
			{leading}
			{trailing}
		</>
	);
}
