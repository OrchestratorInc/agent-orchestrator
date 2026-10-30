import { Stack, useRouter } from "expo-router";
import { useMemo, type ReactElement } from "react";
import { Platform } from "react-native";
import { toolbarIcon } from "./toolbarIcon";
// Material Symbols has no pull request, so Android draws lucide's, the glyph the
// PR cards use, from a bundled bitmap.
import PULL_REQUEST_GLYPH from "../assets/icons/pull-request.png";

/** What the badge prints; past this it stops growing. */
const MAX_BADGE = 99;

// iOS draws the board's own pair as the Settings app's gear and the bell, as
// bare glyphs without iOS 26's shared glass capsule. Android keeps its
// Material settings and notifications glyphs.
const SETTINGS_ICON = toolbarIcon({ ios: "gear", android: "settings" });
const NOTIFICATIONS_ICON = toolbarIcon({ ios: "bell", android: "notifications" });
// SF Symbols' own pull request, so it has the bar's weight and size like the
// gear beside it.
const PULL_REQUESTS_ICON = Platform.OS === "ios" ? "arrow.triangle.pull" : PULL_REQUEST_GLYPH;

export type BoardHeaderItemsProps = {
	unread: number;
	notifications?: boolean;
	/**
	 * The tab's filter: a `Stack.Toolbar.Menu` element, memoised by the page. iOS
	 * leads the bar with it, across from the actions. Android's top app bar has
	 * no leading actions (an item there takes the title's place), so it heads the
	 * trailing ones instead.
	 */
	filter?: ReactElement | null;
	/**
	 * The tab's create action, ending the bar as a "+" on iOS, which has no
	 * floating button. Android draws no header item for it: the page floats
	 * Material's FAB (`AddFab`) with the same action.
	 */
	onAdd?: () => void;
	/** What the "+" does, for VoiceOver. */
	addLabel?: string;
};

/**
 * The board tabs' header items: Settings, Pull Requests, and Notifications
 * with its unread count, plus a tab's own filter and create action. Android
 * leaves out Settings and Pull Requests, which are tabs there. Native bar-button items on
 * iOS, Compose icon buttons on Android.
 *
 * Pull requests and notifications come from a paired machine, so a board with
 * none shows Settings alone; pairing starts from there.
 *
 * Memoised on what it shows: the tabs re-render on every poll, and each new
 * toolbar element would register the header's options again.
 */
export function BoardHeaderItems({ unread, notifications = true, filter = null, onAdd, addLabel = "Add" }: BoardHeaderItemsProps) {
	const router = useRouter();
	const ios = Platform.OS === "ios";
	const leading = useMemo(() => (
		ios && filter ? <Stack.Toolbar placement="left">{filter}</Stack.Toolbar> : null
	), [filter, ios]);
	const trailing = useMemo(() => (
		<Stack.Toolbar placement="right">
			{!ios ? filter : null}
			{/* Android has Settings and Pull Requests as tabs. */}
			{ios ? <Stack.Toolbar.Button icon={SETTINGS_ICON} hidesSharedBackground accessibilityLabel="Settings" onPress={() => router.push("/settings")} /> : null}
			{ios && notifications ? (
				<Stack.Toolbar.Button icon={PULL_REQUESTS_ICON} iconRenderingMode="template" hidesSharedBackground accessibilityLabel="Pull Requests" onPress={() => router.push("/prs")} />
			) : null}
			{notifications ? (
				<Stack.Toolbar.Button
					icon={NOTIFICATIONS_ICON}
					hidesSharedBackground
					// iOS draws bar-item badges only from iOS 26, so the count is in the label too.
					accessibilityLabel={unread > 0 ? `Notifications, ${unread} unread` : "Notifications"}
					onPress={() => router.navigate("/notifications")}
				>
					{unread > 0 ? <Stack.Toolbar.Badge>{unread > MAX_BADGE ? `${MAX_BADGE}+` : String(unread)}</Stack.Toolbar.Badge> : null}
				</Stack.Toolbar.Button>
			) : null}
			{ios && onAdd ? <Stack.Toolbar.Button icon={toolbarIcon("add")} accessibilityLabel={addLabel} onPress={onAdd} /> : null}
		</Stack.Toolbar>
	), [addLabel, filter, ios, notifications, onAdd, router, unread]);
	return (
		<>
			{leading}
			{trailing}
		</>
	);
}
