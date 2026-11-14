import { Feather } from "../icons";
import { StyleSheet, Text } from "react-native";
import type { DashboardPR, SessionPRSummary } from "../api";
import { AppSymbol } from "../AppSymbol";
import { resolveChromeIcon, type ChromeIcon } from "../chromeIcon";
import type { Theme } from "../theme";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { prReviewPromptHeadline, toneColor } from "../prView";
import { fontScaleCap, iconSize, space, type } from "../tokens";

/** Waiting on CI or a reviewer. */
const pending = { ios: "clock", android: "schedule" } as const;

/** What the composer's PR chip says to a screen reader. */
export function prChipAccessibilityLabel(pr: DashboardPR, summary?: SessionPRSummary): string {
	return `Pull request ${pr.number}, ${prReviewPromptHeadline(pr, summary).text}`;
}

/**
 * The session's pull request inside one of the composer's chips: its number and
 * the one status that decides what to do next, in that status's colour. The
 * chip opens the review, where the rest of the PR's state lives.
 */
export function PRChipContent({ pr, summary }: { pr: DashboardPR; summary?: SessionPRSummary }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const headline = prReviewPromptHeadline(pr, summary);
	const color = toneColor(t, headline.tone);
	return <>
		<Feather name="git-pull-request" size={iconSize.xs} color={t.accent} />
		<Text numberOfLines={1} maxFontSizeMultiplier={fontScaleCap.chrome} style={styles.number}>#{pr.number}</Text>
		<StatusGlyph icon={statusIcon(headline.text)} color={color} />
		<Text numberOfLines={1} maxFontSizeMultiplier={fontScaleCap.chrome} style={[styles.status, { color }]}>{headline.text}</Text>
	</>;
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
	number: { fontFamily: "DMSans_600SemiBold", color: t.textPrimary, fontSize: type.footnote.fontSize, fontWeight: "600", marginRight: space.xxs },
	status: { fontFamily: "DMSans_500Medium", flexShrink: 1, fontSize: type.footnote.fontSize, fontWeight: "500" },
});
