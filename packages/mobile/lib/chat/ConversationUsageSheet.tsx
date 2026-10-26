import { StyleSheet, Text, View } from "react-native";
import { rowDividerWidth } from "../divider";
import type { Theme } from "../theme";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { radius, space, type } from "../tokens";
import { SheetScreen } from "../ui";
import { compactContextUsageLabel, compactTokenCount, contextReadout, contextUsageLabel, resetLabel, type Severity } from "./conversationChrome";
import type { ConversationSnapshot } from "./types";

/**
 * The conversation's context window, token counts and account limit, as a
 * small details sheet off the conversation menu and the quota banner.
 */
export function ConversationUsageSheet({ sessionTitle, snapshot }: { sessionTitle: string; snapshot: ConversationSnapshot }) {
	const styles = useThemedStyles(makeStyles);
	const usage = snapshot.usage;
	const limits = snapshot.rateLimits;
	const context = contextReadout(usage);
	const limitPercent = limits && Number.isFinite(limits.primaryUsedPercent) ? Math.min(100, Math.max(0, Math.round(limits.primaryUsedPercent))) : undefined;
	const reset = resetLabel(limits?.primaryResetsInSeconds);
	return (
		<SheetScreen title="Context & usage" subtitle={sessionTitle}>
			<View style={styles.rows}>
				{usage ? (
					<UsageRow
						label="Context window"
						value={context?.percent !== undefined ? `${context.percent}%` : undefined}
						detail={compactContextUsageLabel(usage)}
						detailLabel={contextUsageLabel(usage)}
						meter={context?.percent !== undefined && context.fillPercent !== undefined ? { percent: context.percent, fill: context.fillPercent, severity: context.severity } : undefined}
					/>
				) : null}
				{usage ? <UsageRow label="Tokens" value={`${compactTokenCount(usage.inputTokens)} in · ${compactTokenCount(usage.outputTokens)} out`} /> : null}
				{usage?.cost != null ? <UsageRow label="Cost" value={`${usage.currency || "$"}${usage.cost.toFixed(4)}`} /> : null}
				{limits && limitPercent !== undefined ? (
					<UsageRow
						label={limits.planLabel || "Primary limit"}
						value={`${limitPercent}% used`}
						detail={reset ? `Resets in ${reset}` : undefined}
						meter={{ percent: limitPercent, fill: Math.max(2, limitPercent), severity: limitPercent >= 90 ? "critical" : limitPercent >= 75 ? "warn" : "normal" }}
					/>
				) : null}
				{!usage && !limits ? <Text style={styles.detail}>The agent hasn't reported usage for this conversation yet.</Text> : null}
			</View>
		</SheetScreen>
	);
}

function UsageRow({ label, value, detail, detailLabel, meter }: {
	label: string;
	value?: string;
	detail?: string;
	/** What screen readers say for the detail line, when it abbreviates. */
	detailLabel?: string;
	meter?: { percent: number; fill: number; severity: Severity };
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const meterColor = meter?.severity === "critical" ? t.red : meter?.severity === "warn" ? t.amber : t.accent;
	return (
		<View style={styles.row}>
			<View style={styles.heading}>
				<Text style={styles.label}>{label}</Text>
				{value ? <Text style={styles.value}>{value}</Text> : null}
			</View>
			{meter ? (
				<View
					accessible
					accessibilityRole="progressbar"
					accessibilityLabel={label}
					accessibilityValue={{ min: 0, max: 100, now: meter.percent, text: `${meter.percent}% used` }}
					style={styles.track}
				>
					<View style={[styles.fill, { width: `${meter.fill}%`, backgroundColor: meterColor }]} />
				</View>
			) : null}
			{detail ? <Text accessibilityLabel={detailLabel} style={styles.detail}>{detail}</Text> : null}
		</View>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	rows: { marginTop: space.lg },
	row: { gap: space.xs, paddingVertical: space.md, borderTopWidth: rowDividerWidth, borderTopColor: t.borderSubtle },
	heading: { flexDirection: "row", alignItems: "baseline", justifyContent: "space-between", gap: space.md },
	label: { flexShrink: 1, fontFamily: "DMSans_500Medium", color: t.textPrimary, fontSize: type.subheadline.fontSize, lineHeight: type.subheadline.lineHeight, fontWeight: "500" },
	value: { fontFamily: "DMSans_400Regular", color: t.textSecondary, fontSize: type.subheadline.fontSize, lineHeight: type.subheadline.lineHeight, fontVariant: ["tabular-nums"] },
	detail: { fontFamily: "DMSans_400Regular", color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
	track: { height: space.xxs, borderRadius: radius.xs, backgroundColor: t.bgSubtle, overflow: "hidden" },
	fill: { height: space.xxs, borderRadius: radius.xs },
});
