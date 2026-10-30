import { useRouter } from "expo-router";
import { NativeTabs } from "expo-router/unstable-native-tabs";
import { useMemo } from "react";
import { Platform } from "react-native";
import { boardTitle, workersNeedingYou } from "../../lib/board-tabs";
import { rippleColor } from "../../lib/pressFeedback";
import { startNewTask } from "../../lib/newTaskAction";
import { useApp } from "../../lib/store";
import { symbols } from "../../lib/symbols";
import { useTheme } from "../../lib/ThemeProvider";
// Material Symbols has no pull request, so the tab draws lucide's, as the
// board's bar did, from a bundled bitmap.
import PULL_REQUEST_GLYPH from "../../assets/icons/pull-request.png";
/**
 * The board: Workers and Projects as the platform's own tab bar. On iOS Pull
 * Requests and Settings open from the header, as sheets over the board; on
 * Android they are tabs of their own.
 * UITabBarController on iOS (a sidebar on iPad), Material navigation bar on
 * Android.
 *
 * Every tab stays mounted, so each keeps its list and scroll position while
 * another is showing. Tapping the current tab again scrolls its list to the top
 * natively, and the list has to be its page's first view for that.
 */
export default function BoardTabsLayout() {
	const t = useTheme();
	const router = useRouter();
	const { allSessions } = useApp();
	const waiting = workersNeedingYou(allSessions);
	// iOS labels stay in the system font at the system size, as in Apple's own
	// bars: any font family there also takes React Native's 14pt default and
	// swells the bar. Android's Material bar takes the app's face.
	const labelFont = Platform.OS === "ios" ? {} : { fontFamily: "DMSans_500Medium" };
	// Android's tabs lay their pages out under the clear bar and keep their
	// content clear of it themselves (`useTabBarFrost`), rather than being
	// ended above it. iOS's tabs keep the system's own insets.
	const android = Platform.OS === "android";
	// The store changes on every poll; the tabs only need to change with the
	// theme or the badge.
	return useMemo(() => (
		<NativeTabs
			sidebarAdaptable
			minimizeBehavior="onScrollDown"
			tintColor={t.accent}
			iconColor={{ default: t.textSecondary, selected: t.accent }}
			labelStyle={{
				default: { ...labelFont, color: t.textSecondary },
				selected: { ...labelFont, color: t.accent },
			}}
			// iOS keeps the system material behind the bar (glass on iOS 26), which
			// already follows the theme's light or dark. Android's bar is clear:
			// each tab runs its page on under it and lays the bar's frost over
			// the rows there (see `frost`), so they dissolve into the bar as they
			// pass under it, as iOS's do into its glass.
			backgroundColor={Platform.OS === "android" ? "transparent" : undefined}
			indicatorColor={t.accentTint}
			// Every destination keeps its label, as Material 3's bar specifies:
			// past three tabs the platform's bar otherwise labels only the
			// selected one.
			labelVisibilityMode="labeled"
			rippleColor={rippleColor(t)}
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
			{/* Pull Requests and Settings are tabs on Android, where Material's
			    bar holds up to five destinations and its top app bar is no place
			    for a page's way in. iOS opens both as sheets from the board's bar,
			    and never shows these. */}
			<NativeTabs.Trigger name="(pull-requests)" hidden={!android} disableAutomaticContentInsets={android}>
				<NativeTabs.Trigger.Icon src={PULL_REQUEST_GLYPH} />
				<NativeTabs.Trigger.Label>{boardTitle("(pull-requests)")}</NativeTabs.Trigger.Label>
			</NativeTabs.Trigger>
			<NativeTabs.Trigger name="(preferences)" hidden={!android} disableAutomaticContentInsets={android}>
				<NativeTabs.Trigger.Icon md={symbols.settings.android} />
				<NativeTabs.Trigger.Label>{boardTitle("(preferences)")}</NativeTabs.Trigger.Label>
			</NativeTabs.Trigger>
			{/* New task in the tab bar's own row: iOS 26 sets the search role's
			    item apart from the tabs as a round button, and the compose glyph
			    Notes and Mail use replaces the magnifying glass. Disabled, so a
			    press never selects it; the press opens the New task page the way the
			    page under it would.
			    Android keeps its floating New button and never shows this tab:
			    the role is iOS's, and `hidden` stays fixed so the tabs never
			    remount. */}
			<NativeTabs.Trigger
				name="compose"
				role="search"
				hidden={Platform.OS !== "ios"}
				disabled
				accessibilityLabel="New task"
				listeners={{ tabPress: () => startNewTask(() => router.push("/spawn")) }}
			>
				<NativeTabs.Trigger.Icon sf="square.and.pencil" md="add" />
			</NativeTabs.Trigger>
		</NativeTabs>
	), [android, router, t, waiting]);
}
