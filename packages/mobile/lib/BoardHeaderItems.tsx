import { Stack } from "expo-router";
import { useMemo, type ReactElement } from "react";
import { Platform } from "react-native";
import { useTheme } from "./ThemeProvider";
import { toolbarIcon } from "./toolbarIcon";

// New task, in the compose glyph Notes and Mail use (desktop: SquarePen).
const NEW_TASK_ICON = toolbarIcon({ ios: "square.and.pencil", android: "edit_square" });

export type BoardHeaderItemsProps = {
	/**
	 * The page's filter: a `Stack.Toolbar.Menu` element, memoised by the page.
	 * iOS leads the bar with it, across from New task; Android's top app bar
	 * has no leading actions (its back button is there), so it heads the
	 * trailing ones.
	 */
	filter?: ReactElement | null;
	/** New task, ending the bar. Absent when there is nowhere to start one. */
	onNewTask?: () => void;
};

/**
 * The Workers board's header items: its filter and New task. Settings, Pull
 * Requests and Notifications are home's rows, as they are the desktop
 * sidebar's.
 *
 * Memoised on what it shows: the board re-renders on every poll, and each new
 * toolbar element would register the header's options again.
 */
export function BoardHeaderItems({ filter = null, onNewTask }: BoardHeaderItemsProps) {
	const t = useTheme();
	const ios = Platform.OS === "ios";
	const ink = ios ? t.textPrimary : undefined;
	const leading = useMemo(() => (
		ios && filter ? <Stack.Toolbar placement="left">{filter}</Stack.Toolbar> : null
	), [filter, ios]);
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
