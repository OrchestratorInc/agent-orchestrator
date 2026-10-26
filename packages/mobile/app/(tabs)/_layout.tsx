import { NativeTabs } from "expo-router/unstable-native-tabs";
import { useMemo } from "react";
import { Platform } from "react-native";
import { boardTitle, workersNeedingYou } from "../../lib/board-tabs";
import { rippleColor } from "../../lib/pressFeedback";
import { useApp } from "../../lib/store";
import { symbols, type IOSSymbol } from "../../lib/symbols";
import { useTheme } from "../../lib/ThemeProvider";
// SF Symbols draws a pull request; Material Symbols has none, so Android keeps
// lucide's, the glyph the notifications and the PR page use. It is a bundled
// bitmap because NativeTabs decodes only bitmaps there, not vector drawables
// (scripts/generate-lucide-tab-icon.mjs writes it).
import PULL_REQUEST_GLYPH from "../../assets/icons/tab-pull-request.png";

const PULL_REQUEST_SYMBOL: IOSSymbol = "arrow.triangle.pull";

/**
 * The board: Workers, Projects and Pull Requests as the platform's own tab bar.
 * UITabBarController on iOS (a sidebar on iPad), Material navigation bar on
 * Android.
 *
 * Every tab stays mounted, so each keeps its list and scroll position while
 * another is showing. Tapping the current tab again scrolls its list to the top
 * natively, and the list has to be its page's first view for that.
 */
export default function BoardTabsLayout() {
	const t = useTheme();
	const { allSessions } = useApp();
	const waiting = workersNeedingYou(allSessions);
	// The store changes on every poll; the tabs only need to change with the
	// theme or the badge.
	return useMemo(() => (
		<NativeTabs
			sidebarAdaptable
			minimizeBehavior="onScrollDown"
			tintColor={t.accent}
			iconColor={{ default: t.textSecondary, selected: t.accent }}
			labelStyle={{
				default: { fontFamily: "Geist_500Medium", color: t.textSecondary },
				selected: { fontFamily: "Geist_500Medium", color: t.accent },
			}}
			// iOS keeps the system material behind the bar (glass on iOS 26), which
			// already follows the theme's light or dark. Android's bar is a flat
			// surface, so it takes the theme's own chrome colour.
			backgroundColor={Platform.OS === "android" ? t.bgSurface : undefined}
			indicatorColor={t.accentTint}
			rippleColor={rippleColor(t)}
		>
			<NativeTabs.Trigger name="(workers)">
				<NativeTabs.Trigger.Icon sf={symbols.workers.ios} md={symbols.workers.android} />
				<NativeTabs.Trigger.Label>{boardTitle("(workers)")}</NativeTabs.Trigger.Label>
				{waiting > 0 ? <NativeTabs.Trigger.Badge>{String(waiting)}</NativeTabs.Trigger.Badge> : null}
			</NativeTabs.Trigger>
			<NativeTabs.Trigger name="(projects)">
				<NativeTabs.Trigger.Icon sf={{ default: symbols.projects.ios, selected: "folder.fill" }} md={symbols.projects.android} />
				<NativeTabs.Trigger.Label>{boardTitle("(projects)")}</NativeTabs.Trigger.Label>
			</NativeTabs.Trigger>
			<NativeTabs.Trigger name="(prs)">
				<NativeTabs.Trigger.Icon sf={PULL_REQUEST_SYMBOL} src={PULL_REQUEST_GLYPH} />
				<NativeTabs.Trigger.Label>{boardTitle("(prs)")}</NativeTabs.Trigger.Label>
			</NativeTabs.Trigger>
		</NativeTabs>
	), [t, waiting]);
}
