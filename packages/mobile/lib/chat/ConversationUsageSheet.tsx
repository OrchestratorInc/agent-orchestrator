import { StyleSheet, Text, View } from "react-native";
import { rowDividerWidth } from "../divider";
import type { Theme } from "../theme";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { radius, space, type } from "../tokens";
import { SheetScreen } from "../ui";
import { compactContextUsageLabel, compactTokenCount, contextReadout, contextUsageLabel, quotaWindows, resetLabel, type Severity } from "./conversationChrome";
import type { ConversationSnapshot } from "./types";

/**
 * The conversation's context window, token counts and account limit, as a
 * small details sheet off the conversation menu and the quota chip.
 */
export function ConversationUsageSheet({ sessionTitle, snapshot }: { sessionTitle: string; snapshot: ConversationSnapshot }) {
	const styles = useThemedStyles(makeStyles);
	const usage = snapshot.usage;
	const limits = snapshot.rateLimits;
	const context = contextReadout(usage);
	// Every window the provider reported: the chip warns on the fullest, which is
	// not always the primary one.
	const windows = quotaWindows(limits);
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
				{windows.map((window) => {
					const reset = resetLabel(window.resetsInSeconds);
					return (
						<UsageRow
							key={window.key}
							label={window.label}
							value={`${window.percent}% used`}
							detail={reset ? `Resets in ${reset}` : undefined}
							meter={{ percent: window.percent, fill: window.fillPercent, severity: window.severity }}
						/>
					);
				})}
				{!usage && windows.length === 0 ? <Text style={styles.detail}>The agent hasn't reported usage for this conversation yet.</Text> : null}
			</View>
		</SheetScreen>
	);
}

/** One measured line: label and value, an optional meter, and a detail under them. */
export function UsageRow({ label, value, detail, detailLabel, meter }: {
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
