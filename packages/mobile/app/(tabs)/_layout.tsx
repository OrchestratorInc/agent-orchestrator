import { usePathname, useRouter } from "expo-router";
import { NativeTabs } from "expo-router/unstable-native-tabs";
import { useMemo, useRef } from "react";
import { Platform, StyleSheet, View } from "react-native";
import { BoardTabBar } from "../../lib/BoardTabBar";
import { reselectBoardTab } from "../../lib/boardTabReselect";
import { boardTabs, boardTitle, workersNeedingYou, type BoardTabName } from "../../lib/board-tabs";
import { startNewTask } from "../../lib/newTaskAction";
import { useApp } from "../../lib/store";
import { symbols } from "../../lib/symbols";
import { useTheme } from "../../lib/ThemeProvider";
/**
 * The board: Workers and Projects as tabs, New task as a round button set apart
 * at the bar's end, and Pull Requests and Settings in the header, on both
 * platforms. iOS draws the bar as UITabBarController's own (a sidebar on iPad).
 * Android hides Material's navigation bar, which shares the whole width out
 * between its items, and floats `BoardTabBar` over the tabs instead.
 *
 * Every tab stays mounted, so each keeps its list and scroll position while
 * another is showing. Tapping the current tab again scrolls its list to the
 * top: natively on iOS, where the list has to be its page's first view for
 * that, and through `boardTabReselect` on Android.
 */
export default function BoardTabsLayout() {
	const t = useTheme();
	const router = useRouter();
	const { allSessions } = useApp();
	const waiting = workersNeedingYou(allSessions);
	// Android's tabs lay their pages out under the floating bar and keep their
	// content clear of it themselves (`useTabBarFrost`). iOS's tabs keep the
	// system's own insets.
	const android = Platform.OS === "android";
	// The tab on screen, for Android's bar. A page pushed over the board has a
	// path of its own and covers the bar, so the last board tab stays selected
	// under it.
	const pathname = usePathname();
	const lastTab = useRef<BoardTabName>("(workers)");
	lastTab.current = boardTabs.find((tab) => tab.href === pathname)?.name ?? lastTab.current;
	const selectedTab = lastTab.current;
	// The store changes on every poll; the tabs only need to change with the
	// theme or the badge.
	const tabs = useMemo(() => (
		<NativeTabs
			// Android's bar is `BoardTabBar`; the tabs stay, without Material's.
			hidden={android}
			sidebarAdaptable
			minimizeBehavior="onScrollDown"
			// iOS labels stay in the system font at the system size, as in Apple's
			// own bars, over the system material (glass on iOS 26), which already
			// follows the theme's light or dark.
			tintColor={t.accent}
			iconColor={{ default: t.textSecondary, selected: t.accent }}
			labelStyle={{ default: { color: t.textSecondary }, selected: { color: t.accent } }}
		>
			<NativeTabs.Trigger name="(workers)" disableAutomaticContentInsets={android}>
				{/* Outlined at rest and filled once selected, as Apple's own tab
				    bars draw them. */}
				<NativeTabs.Trigger.Icon sf={{ default: "square.stack.3d.up", selected: "square.stack.3d.up.fill" }} md={symbols.workers.android} />
				<NativeTabs.Trigger.Label>{boardTitle("(workers)")}</NativeTabs.Trigger.Label>
				{waiting > 0 ? <NativeTabs.Trigger.Badge>{String(waiting)}</NativeTabs.Trigger.Badge> : null}
			</NativeTabs.Trigger>
			<NativeTabs.Trigger name="(projects)" disableAutomaticContentInsets={android}>
				<NativeTabs.Trigger.Icon sf={{ default: "folder", selected: "folder.fill" }} md={symbols.projects.android} />
				<NativeTabs.Trigger.Label>{boardTitle("(projects)")}</NativeTabs.Trigger.Label>
			</NativeTabs.Trigger>
			{/* New task in the tab bar's own row: iOS 26 sets the search role's
			    item apart from the tabs as a round button, and the compose glyph
			    Notes and Mail use replaces the magnifying glass. Disabled, so a
			    press never selects it; the press opens the New task page the way the
			    page under it would.
			    Android draws its own round button in `BoardTabBar` and never
			    shows this tab: the role is iOS's, and `hidden` stays fixed so the
			    tabs never remount. */}
			<NativeTabs.Trigger
				name="compose"
				role="search"
				hidden={Platform.OS !== "ios"}
				disabled
				accessibilityLabel="New task"
				listeners={{ tabPress: () => startNewTask(() => router.push("/spawn")) }}
			>
				<NativeTabs.Trigger.Icon sf="square.and.pencil" md="add" />
				{/* Named for what it starts, not the route: untitled, the bar
				    labelled it "compose". */}
				<NativeTabs.Trigger.Label>New task</NativeTabs.Trigger.Label>
			</NativeTabs.Trigger>
		</NativeTabs>
	), [android, router, t, waiting]);
	if (!android) return tabs;
	return (
		<View style={styles.board}>
			{tabs}
			<BoardTabBar
				selected={selectedTab}
				// Tapping the tab on screen again scrolls its list to the top, as
				// iOS's bar does natively.
				onSelect={(name) => name === selectedTab
					? reselectBoardTab(name)
					: router.navigate(boardTabs.find((tab) => tab.name === name)!.href)}
				waiting={waiting}
				onNewTask={() => startNewTask(() => router.push("/spawn"))}
			/>
		</View>
	);
}

const styles = StyleSheet.create({
	board: { flex: 1 },
});
