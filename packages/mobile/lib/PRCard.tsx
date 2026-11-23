import { MenuView, type MenuAction, type MenuComponentRef, type NativeActionEvent } from "@expo/ui/community/menu";
import * as Clipboard from "expo-clipboard";
import { useRouter } from "expo-router";
import { memo, useRef, useState } from "react";
import { AppSymbol } from "./AppSymbol";
import { Feather } from "./icons";
import { useOpenPage } from "./pageNavigation";
import { Platform, Pressable, StyleSheet, Text, View, type AccessibilityActionEvent, type GestureResponderEvent } from "react-native";
import { sessionTitle, type DashboardPR, type DashboardSession, type SessionPRSummary } from "./api";
import { rowDividerWidth } from "./divider";
import { endsGroupedSection, groupedRowCorners, type GroupedRowPosition } from "./groupedRow";
import { haptics } from "./haptics";
import { relativeTime } from "./notificationView";
import { openGitHub } from "./openGitHub";
import { prCardMenuIcons } from "./pr-card-menu-icons";
import { usePressFeedback } from "./pressFeedback";
import type { Theme } from "./theme";
import { prRowLifecycle, prRowStatus, prRowTitle, prTitle, stateVisualOf, toneColor } from "./prView";
import { reviewRouteForPR, workerRouteForSession } from "./reviewView";
import { useTheme, useThemeState, useThemedStyles } from "./ThemeProvider";
import { useMenuRowWidth } from "./useMenuRowWidth";
import { fontScaleCap, groupedCorner, gutter, iconSize, space, type } from "./tokens";

type PRCardAction = "openWorker" | "openInGitHub" | "copyLink";

const ACTION_TITLES: Record<PRCardAction, string> = {
	openWorker: "Open worker",
	openInGitHub: "Open in GitHub",
	copyLink: "Copy link",
};

/**
 * A pull-request row in an inset-grouped list: the state glyph, the title over
 * up to two lines, and one line under it with the number and only the status
 * that needs you. The age sits at the trailing edge, with iOS's disclosure
 * chevron since the row opens a page.
 *
 * `position` says where the row sits in its section, so it draws its slice of
 * the section's card: the rounded ends, and a hairline (iOS) or gap (Android)
 * between rows.
 *
 * A tap opens the review; the long press reaches the worker too. Everything else about the pull request is in the
 * row's long-press menu — a SwiftUI context menu on iOS, a Material dropdown on
 * Android — and in its accessibility actions, so a screen reader reaches the
 * same choices from the one row.
 */
export const PRCard = memo(function PRCard({
	pr,
	session,
	summary,
	hostId,
	hostName,
	showProject = false,
	position = "only",
	now,
	onLinkCopied,
}: {
	pr: DashboardPR;
	session: DashboardSession;
	summary?: SessionPRSummary;
	hostId: string;
	hostName?: string;
	/** Name the repository on the row: on when the list spans more than one. */
	showProject?: boolean;
	position?: GroupedRowPosition;
	/** The current time, passed so the row's age re-renders as it changes. */
	now?: number;
	/** Called once the pull request's link is on the clipboard. */
	onLinkCopied?: () => void;
}) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const styles = useThemedStyles(makeStyles);
	const feedback = usePressFeedback("row");
	const openPage = useOpenPage();
	const router = useRouter();
	const menu = useRef<MenuComponentRef>(null);
	const menuWidth = useMenuRowWidth();
	// Android: where the finger came down, and the menu's anchor placed there.
	const touch = useRef({ x: 0, y: 0 });
	const [anchor, setAnchor] = useState({ x: 0, y: 0 });
	// From the board, as the row's section is: see prRowLifecycle.
	const state = stateVisualOf(t, prRowLifecycle(pr, summary));
	const { title, scope } = prRowTitle(summary?.title?.trim() || prTitle(pr, sessionTitle(session)));
	// The repository's own name, without its owner.
	const project = (summary?.repo || session.projectId || "Standalone").split("/").pop() || "Standalone";
	const place = [hostName, showProject || hostName ? project : undefined].filter(Boolean).join(" · ");
	const status = prRowStatus(pr, summary);
	// Where the pull request is, then what kind of change it is: the scope only
	// fills the line when no status needs it.
	const context = place || (status ? "" : scope ?? "");
	const age = summary?.updatedAt ? relativeTime(summary.updatedAt, now) : "";
	const ends = endsGroupedSection(position);
	const link = summary?.htmlUrl || summary?.url || pr.url;

	const run = (action: string) => {
		if (action === "openWorker") router.push(workerRouteForSession(session, hostId));
		if (action === "openInGitHub") void openGitHub(link);
		if (action === "copyLink") {
			void Clipboard.setStringAsync(link);
			haptics.success();
			onLinkCopied?.();
		}
	};
	const actions: readonly PRCardAction[] = link ? ["openWorker", "openInGitHub", "copyLink"] : ["openWorker"];
	const menuActions: MenuAction[] = actions.map((id) => ({ id, title: ACTION_TITLES[id], image: prCardMenuIcons[id], imageColor: t.textSecondary }));

	const row = (
		<Pressable
			accessibilityRole="button"
			accessibilityLabel={[
				title,
				context,
				`Pull request ${pr.number}`,
				status?.text ?? state.label,
				age ? (age === "now" ? "Updated just now" : `Updated ${age} ago`) : "",
			].filter(Boolean).join(". ") + "."}
			accessibilityActions={actions.map((name) => ({ name, label: ACTION_TITLES[name] }))}
			onAccessibilityAction={(event: AccessibilityActionEvent) => run(event.nativeEvent.actionName)}
			android_ripple={feedback.android_ripple}
			onPress={() => {
				openPage(reviewRouteForPR(session.id, pr, hostId));
			}}
			// The row claims the touch, so on Android its long press opens the
			// menu itself, at the finger; iOS's context menu takes the gesture natively.
			onPressIn={Platform.OS === "android" ? (event: GestureResponderEvent) => { touch.current = { x: event.nativeEvent.locationX, y: event.nativeEvent.locationY }; } : undefined}
			onLongPress={Platform.OS === "android" ? () => {
				setAnchor(touch.current);
				// Opened once the anchor has moved under the finger.
				requestAnimationFrame(() => menu.current?.show());
			} : undefined}
			style={({ pressed }) => [styles.row, groupedRowCorners(position), menuWidth.style, pressed && feedback.pressed]}
		>
			<View style={styles.glyph}>
				<Feather name="git-pull-request" size={iconSize.lg} color={state.color} />
			</View>
			<View style={[styles.content, !ends && styles.divided]}>
				<View style={styles.copy}>
					<Text style={styles.title} numberOfLines={2}>{title}</Text>
					<Text style={styles.subtitle} numberOfLines={1}>
						{`#${pr.number}`}
						{status ? " · " : null}
						{status ? <Text style={{ color: status.tone === "neutral" ? t.textSecondary : toneColor(t, status.tone) }}>{status.text}</Text> : null}
						{context ? ` · ${context}` : null}
					</Text>
				</View>
				<View style={styles.trailing}>
					{age ? <Text style={styles.age}>{age}</Text> : null}
					{Platform.OS === "ios" ? <AppSymbol name="chevronForward" size={iconSize.xs} color={t.textFaint} weight="semibold" /> : null}
				</View>
			</View>
		</Pressable>
	);

	// On Android the menu sits beside the row, anchored where the finger is, as
	// a worker row's does: wrapped around the row, its Compose host laid the
	// eyebrow out past the row's right padding and clipped the status.
	if (Platform.OS === "android") {
		return (
			<View style={[styles.slot, !ends && styles.joined]}>
				{row}
				<MenuView
					ref={menu}
					colorScheme={scheme}
					actions={menuActions}
					onPressAction={(event: NativeActionEvent) => run(event.nativeEvent.event)}
					style={[styles.menuAnchor, { left: anchor.x, top: anchor.y }]}
					testID="pr-card-menu"
				>
					<View pointerEvents="none" style={styles.menuPoint} />
				</MenuView>
			</View>
		);
	}

	return (
		<View ref={menuWidth.ref} style={styles.slot} onLayout={menuWidth.onLayout}>
			<MenuView
				ref={menu}
				colorScheme={scheme}
				actions={menuActions}
				shouldOpenOnLongPress
				onPressAction={(event: NativeActionEvent) => run(event.nativeEvent.event)}
				testID="pr-card-menu"
			>
				{row}
			</MenuView>
		</View>
	);
});

/** A section's heading over its card, in line with the rows' text, with its count. */
export function PRSectionHeader({ label, count }: { label: string; count: number }) {
	const styles = useThemedStyles(makeStyles);
	return (
		<View accessible accessibilityRole="header" accessibilityLabel={`${label}, ${count}`} style={styles.sectionHeader}>
			<Text maxFontSizeMultiplier={fontScaleCap.chrome} style={styles.sectionLabel}>{label}</Text>
			<Text maxFontSizeMultiplier={fontScaleCap.chrome} style={styles.sectionCount}>{count}</Text>
		</View>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		// Inset from the screen's edges, as an inset-grouped section is. Rows in
		// a section meet flush on iOS and keep a small gap on Android.
		slot: { marginHorizontal: gutter },
		joined: { marginBottom: groupedCorner.gap },
		// Opaque, so the row iOS lifts out for its context menu is not
		// see-through; clipped, so the press highlight and ripple keep its corners.
		row: {
			flexDirection: "row",
			paddingLeft: gutter,
			backgroundColor: t.bgElevated,
			overflow: "hidden",
			borderCurve: "continuous",
		},
		// Centred on the title's first line.
		glyph: { width: iconSize.lg, paddingTop: space.md + (type.headline.lineHeight - iconSize.lg) / 2, marginRight: space.md },
		// Explicit grow, shrink and zero basis: inside the context menu's SwiftUI
		// host a bare `flex: 1` kept the text's intrinsic width, and the title ran
		// off the card on one line instead of wrapping.
		content: { flexGrow: 1, flexShrink: 1, flexBasis: 0, minWidth: 0, flexDirection: "row", gap: space.sm, paddingVertical: space.md, paddingRight: gutter },
		// iOS divides a section's rows with a hairline inset to the text; Android's
		// connected list separates them with the gap instead.
		divided: Platform.OS === "ios" ? { borderBottomWidth: rowDividerWidth, borderBottomColor: t.borderSubtle } : {},
		copy: { flexGrow: 1, flexShrink: 1, flexBasis: 0, minWidth: 0, gap: space.hair },
		title: { ...type.headline, color: t.textPrimary },
		subtitle: { ...type.footnote, color: t.textSecondary },
		trailing: { flexDirection: "row", alignItems: "center", gap: space.xs, alignSelf: "flex-start", minHeight: type.headline.lineHeight },
		age: { ...type.footnote, color: t.textTertiary, fontVariant: ["tabular-nums"] },
		sectionHeader: { flexDirection: "row", alignItems: "baseline", gap: space.xs, paddingHorizontal: gutter * 2, paddingTop: space.xl, paddingBottom: space.sm },
		sectionLabel: { ...type.subheadline, fontFamily: type.headline.fontFamily, fontWeight: type.headline.fontWeight, color: t.textPrimary },
		sectionCount: { ...type.subheadline, color: t.textTertiary, fontVariant: ["tabular-nums"] },
		// Takes no touches: the row under it has them all.
		menuAnchor: { position: "absolute", pointerEvents: "none" },
		menuPoint: { width: 1, height: 1 },
	});
