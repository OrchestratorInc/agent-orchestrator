import { Feather } from "../icons";
import { Pressable, StyleSheet, Text, View } from "react-native";
import type { DashboardPR, SessionPRSummary } from "../api";
import { AppSymbol } from "../AppSymbol";
import { resolveChromeIcon, type ChromeIcon } from "../chromeIcon";
import { usePressFeedback } from "../pressFeedback";
import type { Theme } from "../theme";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { prReviewPromptHeadline, prReviewPromptStatuses, toneColor, type Tone } from "../prView";
import { iconSize, radius, space, touchTarget, type } from "../tokens";

/** Waiting on CI or a reviewer. */
const pending = { ios: "clock", android: "schedule" } as const;

export function PRReviewPrompt({ pr, summary, branch, onPress, onCollapse }: { pr: DashboardPR; summary?: SessionPRSummary; branch?: string; onPress(): void; onCollapse(): void }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const rowFeedback = usePressFeedback("row");
	const iconFeedback = usePressFeedback("icon");
	const title = summary?.title.trim() || pr.title?.trim() || `PR #${pr.number}`;
	const branchName = summary?.sourceBranch?.trim() || pr.branch?.trim() || branch?.trim();
	const headline = prReviewPromptHeadline(pr, summary);
	const statuses = prReviewPromptStatuses(pr, summary).filter((status) => status.text !== "CI passing" && status.text !== headline.text);
	const additions = summary?.additions ?? pr.additions;
	const deletions = summary?.deletions ?? pr.deletions;

	return <View style={styles.wrapper}>
		<Pressable
			accessibilityRole="button"
			accessibilityLabel={`Pull request ${pr.number}, ${title}. ${headline.text}.${additions !== undefined && deletions !== undefined ? ` ${additions} additions, ${deletions} deletions.` : ""}`}
			android_ripple={rowFeedback.android_ripple}
			onPress={onPress}
			style={({ pressed }) => [styles.prompt, pressed && rowFeedback.pressed]}
		>
		<View style={styles.header}>
			<View style={styles.iconTile}><Feather name="git-pull-request" size={19} color={t.accent} /></View>
			<View style={styles.copy}>
				<View style={styles.headline}>
					<StatusGlyph icon={statusIcon(headline.text)} color={toneColor(t, headline.tone)} />
					<Text style={[styles.headlineText, { color: toneColor(t, headline.tone) }]}>{headline.text}</Text>
				</View>
				<Text numberOfLines={1} ellipsizeMode="tail" style={styles.title}>{title}</Text>
				{branchName ? <Text numberOfLines={1} ellipsizeMode="tail" style={styles.meta}>{branchName}</Text> : null}
			</View>
			<View style={styles.trailing}>
				{additions !== undefined && deletions !== undefined ? <View style={styles.diff}>
					<Text style={styles.additions}>+{additions}</Text>
					<Text style={styles.deletions}>−{deletions}</Text>
				</View> : null}
				<AppSymbol name="chevronForward" size={iconSize.sm} color={t.textTertiary} />
			</View>
		</View>
		<View style={styles.statuses}>
			{statuses.map((status) => <StatusFact key={`${status.text}-${status.tone}`} text={status.text} tone={status.tone} />)}
		</View>
		</Pressable>
		{/* Its own button beside the card, not inside it: one control opens the
		    review and the other folds the card into the composer. A downward
		    drag on the card does the same (see ChatComposer). */}
		<Pressable
			accessibilityRole="button"
			accessibilityLabel="Collapse pull request card"
			android_ripple={iconFeedback.android_ripple}
			onPress={onCollapse}
			style={({ pressed }) => [styles.collapse, pressed && iconFeedback.pressed]}
		>
			<AppSymbol name="chevronDown" size={iconSize.md} color={t.textTertiary} />
		</Pressable>
	</View>;
}

function StatusFact({ text, tone }: { text: string; tone: Tone }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const color = toneColor(t, tone);
	return <View style={styles.statusFact}>
		<StatusGlyph icon={statusIcon(text)} color={color} />
		<Text style={[styles.statusText, { color }]}>{text}</Text>
	</View>;
}

/** A status's system symbol, or the Lucide drawing for the pull-request glyphs no system set has. */
function StatusGlyph({ icon, color }: { icon: ChromeIcon; color: string }) {
	const glyph = resolveChromeIcon(icon);
	if (glyph.symbol) return <AppSymbol name={glyph.symbol} size={iconSize.xs} color={color} />;
	return <Feather name={glyph.lucide} size={iconSize.xs} color={color} />;
}

function statusIcon(status: string): ChromeIcon {
	if (status.startsWith("CI passing") || status === "Approved") return "checkCircle";
	if (status.startsWith("CI failing") || status.includes("Conflict") || status === "Blocked" || status === "Changes requested" || status.includes("Unresolved")) return "error";
	if (status.startsWith("CI running") || status.includes("pending") || status === "Unstable") return pending;
	if (status === "Mergeable" || status === "Ready to merge" || status === "Merged") return "git-merge";
	return "git-pull-request";
}

const makeStyles = (t: Theme) => StyleSheet.create({
	wrapper: { width: "100%", flexDirection: "row", alignItems: "flex-start" },
	prompt: { flex: 1, minWidth: 0, gap: space.xs, paddingHorizontal: space.xs, paddingVertical: space.xs, borderRadius: radius.lg, borderCurve: "continuous", overflow: "hidden" },
	collapse: { width: touchTarget, height: touchTarget, alignItems: "center", justifyContent: "center", marginTop: space.xxs },
	header: { minHeight: 54, flexDirection: "row", alignItems: "center", gap: space.md },
	iconTile: { width: 40, height: 40, alignItems: "center", justifyContent: "center", borderRadius: radius.pill, backgroundColor: t.accentTint },
	copy: { flex: 1, minWidth: 0, gap: 2 },
	headline: { flexDirection: "row", alignItems: "center", gap: space.xxs },
	headlineText: { fontFamily: "Geist_500Medium", fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, fontWeight: "500" },
	title: { minWidth: 0, flexShrink: 1, fontFamily: "Geist_600SemiBold", color: t.textPrimary, fontSize: type.subheadline.fontSize, lineHeight: type.subheadline.lineHeight, fontWeight: "600" },
	meta: { minWidth: 0, flexShrink: 1, fontFamily: "Geist_400Regular", color: t.textTertiary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
	trailing: { flexDirection: "row", alignItems: "center", gap: space.xs },
	statuses: { flexDirection: "row", flexWrap: "wrap", alignItems: "center", columnGap: space.md, rowGap: space.xs, paddingLeft: 40 + space.md },
	statusFact: { minHeight: 20, flexDirection: "row", alignItems: "center", gap: space.xxs },
	statusText: { fontFamily: "Geist_500Medium", fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, fontWeight: "500" },
	diff: { flexDirection: "row", alignItems: "center", gap: space.xxs },
	additions: { fontFamily: "Geist_600SemiBold", color: t.green, fontSize: type.footnote.fontSize, fontWeight: "600" },
	deletions: { fontFamily: "Geist_600SemiBold", color: t.red, fontSize: type.footnote.fontSize, fontWeight: "600" },
});
