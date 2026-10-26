import { MenuView, type MenuAction, type MenuComponentRef, type NativeActionEvent } from "@expo/ui/community/menu";
import * as Clipboard from "expo-clipboard";
import { memo, useRef } from "react";
import { Feather } from "./icons";
import { useOpenPage } from "./pageNavigation";
import { Platform, Pressable, StyleSheet, Text, View, type AccessibilityActionEvent } from "react-native";
import { sessionTitle, shortLabel, type DashboardPR, type DashboardSession, type SessionPRSummary } from "./api";
import { rowDividerWidth } from "./divider";
import { haptics } from "./haptics";
import { openGitHub } from "./openGitHub";
import { prCardMenuIcons } from "./pr-card-menu-icons";
import { usePressFeedback } from "./pressFeedback";
import type { Theme } from "./theme";
import {
	prBlockerLine,
	prStateVisual,
	prStatusAtoms,
	prSummaryLine,
	prTitle,
	stateVisualOf,
	toneColor,
	type PRLifecycle,
} from "./prView";
import { useTheme, useThemeState, useThemedStyles } from "./ThemeProvider";
import { iconSize, space, type } from "./tokens";

type PRCardAction = "openInGitHub" | "copyLink";

const ACTION_TITLES: Record<PRCardAction, string> = {
	openInGitHub: "Open in GitHub",
	copyLink: "Copy link",
};

/**
 * A pull-request row with the same hierarchy as WorkerListRow: an eyebrow, the
 * title, then metadata, none of it smaller than footnote.
 *
 * A tap opens the review. Everything else about the pull request is in the
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
	onLinkCopied,
}: {
	pr: DashboardPR;
	session: DashboardSession;
	summary?: SessionPRSummary;
	hostId: string;
	hostName?: string;
	/** Called once the pull request's link is on the clipboard. */
	onLinkCopied?: () => void;
}) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const styles = useThemedStyles(makeStyles);
	const feedback = usePressFeedback("row");
	const openPage = useOpenPage();
	const menu = useRef<MenuComponentRef>(null);
	const state = summary ? stateVisualOf(t, summary.state as PRLifecycle) : prStateVisual(t, pr);
	const title = summary?.title?.trim() || prTitle(pr, sessionTitle(session));
	const project = shortLabel(summary?.repo || session.projectId || "Standalone");
	const projectLabel = hostName ? `${hostName} · ${project}` : project;
	const branches = summary
		? [summary.sourceBranch, summary.targetBranch].filter(Boolean).join(" → ")
		: session.branch || "";
	const diff = summary && (summary.changedFiles > 0 || summary.additions > 0 || summary.deletions > 0)
		? `${summary.changedFiles} ${summary.changedFiles === 1 ? "file" : "files"}  +${summary.additions} −${summary.deletions}`
		: "";
	const detail = [`#${pr.number}`, branches, diff].filter(Boolean).join("  ·  ");
	const atoms = summary ? prStatusAtoms(summary) : [prSummaryLine(pr)];
	const status = atoms[0] ?? { text: state.label, tone: "passive" as const };
	const blockers = summary ? prBlockerLine(summary) : null;
	const link = summary?.htmlUrl || summary?.url || pr.url;

	const run = (action: string) => {
		if (action === "openInGitHub") void openGitHub(link);
		if (action === "copyLink") {
			void Clipboard.setStringAsync(link);
			haptics.success();
			onLinkCopied?.();
		}
	};
	const actions = link ? (["openInGitHub", "copyLink"] as const) : [];
	const menuActions: MenuAction[] = actions.map((id) => ({ id, title: ACTION_TITLES[id], image: prCardMenuIcons[id], imageColor: t.textSecondary }));

	const row = (
		<Pressable
			accessibilityRole="button"
			accessibilityLabel={`${title}. ${projectLabel}. Pull request ${pr.number}. ${status.text}.`}
			accessibilityActions={actions.map((name) => ({ name, label: ACTION_TITLES[name] }))}
			onAccessibilityAction={(event: AccessibilityActionEvent) => run(event.nativeEvent.actionName)}
			android_ripple={feedback.android_ripple}
			onPress={() => {
				openPage({
					pathname: "/review/[sessionId]",
					params: { sessionId: session.id, prNumber: String(pr.number), prUrl: pr.url, hostId },
				});
			}}
			// The row claims the touch, so on Android its long press opens the
			// menu itself; iOS's context menu takes the gesture natively.
			onLongPress={Platform.OS === "android" && actions.length ? () => menu.current?.show() : undefined}
			style={({ pressed }) => [styles.row, pressed && feedback.pressed]}
		>
			<View style={styles.eyebrow}>
				<Feather name="git-pull-request" size={iconSize.sm} color={state.color} />
				<Text style={styles.project} numberOfLines={1}>{projectLabel}</Text>
				<Text style={[styles.status, { color: toneColor(t, status.tone) }]} numberOfLines={1}>{status.text}</Text>
			</View>

			<View style={styles.copy}>
				<Text style={styles.title} numberOfLines={1}>{title}</Text>
				<Text style={styles.details} numberOfLines={1}>{detail}</Text>
			</View>

			{blockers ? <Text style={styles.blockers} numberOfLines={1}>{blockers}</Text> : null}
		</Pressable>
	);

	if (!actions.length) return row;
	return (
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
	);
});

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		// Opaque, so the row iOS lifts out for its context menu is not see-through.
		row: {
			minHeight: 76,
			justifyContent: "center",
			paddingHorizontal: space.lg,
			paddingVertical: space.sm,
			gap: space.hair,
			backgroundColor: t.bgBase,
			borderBottomWidth: rowDividerWidth,
			borderBottomColor: t.borderSubtle,
		},
		eyebrow: { flexDirection: "row", alignItems: "center", gap: space.xs, minHeight: type.footnote.lineHeight },
		project: { fontFamily: "Geist_500Medium", flex: 1, color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, fontWeight: "500" },
		status: { fontFamily: "Geist_500Medium", flexShrink: 0, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, fontWeight: "500" },
		copy: { gap: space.hair },
		title: { fontFamily: "Geist_600SemiBold", color: t.textPrimary, fontSize: type.callout.fontSize, lineHeight: type.callout.lineHeight, fontWeight: "600", letterSpacing: -0.15 },
		details: { color: t.textTertiary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, fontFamily: t.fontMono },
		blockers: { fontFamily: "Geist_400Regular", color: t.amber, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, marginTop: space.hair, marginLeft: space.xl },
	});
