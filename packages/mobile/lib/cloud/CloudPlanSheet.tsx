import { StyleSheet, Text, View } from "react-native";
import { UsageRow } from "../chat/ConversationUsageSheet";
import { rowDividerWidth } from "../divider";
import { Spinner } from "../Spinner";
import type { Theme } from "../theme";
import { useThemedStyles } from "../ThemeProvider";
import { space, type } from "../tokens";
import { SheetScreen } from "../ui";
import { useCloud } from "./CloudProvider";
import { planResetCopy } from "./errors";
import { cloudPlanView, FREE_PLAN_NOTE, PLAN_USAGE_NOTE, planRefusalTitle } from "./plan";
import type { PlanSheetParams } from "./planRefusal";
import { useCloudPlan } from "./planStore";

/**
 * Why the organization's plan refused a request, in Cloud's own words and
 * with when it lifts, over the plan's usage: the same rows the conversation's
 * usage sheet draws. Opened by any refusal, and from New task without one.
 * It sells nothing: plans are chosen on the desktop.
 */
export function CloudPlanSheet({ code, message, resetsAt }: PlanSheetParams) {
	const styles = useThemedStyles(makeStyles);
	const { client, org } = useCloud();
	const plan = useCloudPlan(client, org?.id);
	const view = plan?.summary ? cloudPlanView(plan.summary) : undefined;
	const reason = code ? [message, planResetCopy(resetsAt)].filter(Boolean).join(" ") : undefined;
	return (
		<SheetScreen title={planRefusalTitle(code)} subtitle={code ? undefined : org?.displayName}>
			{reason ? <Text style={styles.reason}>{reason}</Text> : null}
			{plan?.status === "loading" ? (
				<View style={styles.loading}><Spinner accessibilityLabel="Loading your plan" /></View>
			) : null}
			{plan?.status === "failed" ? <Text style={styles.note}>Couldn't load your plan's usage. Try again in a moment.</Text> : null}
			{view?.kind === "free" ? (
				<View style={styles.rows}>
					<UsageRow label="Free plan" value="No cloud agents" />
					<Text style={styles.note}>{FREE_PLAN_NOTE}</Text>
				</View>
			) : null}
			{view?.kind === "paid" ? (
				<View style={styles.rows}>
					<UsageRow label={`${view.name} plan`} value={view.status} detail={view.note} />
					{view.meters.map((meter) => (
						<UsageRow
							key={meter.key}
							label={meter.label}
							value={meter.value}
							detail={meter.detail}
							meter={{ percent: Math.round(meter.fraction * 100), fill: Math.max(2, meter.fraction * 100), severity: meter.severity }}
						/>
					))}
					{view.running ? <UsageRow label="Running sessions" value={view.running.value} detail={view.running.detail} /> : null}
					{view.total ? <UsageRow label="Total sessions" value={view.total.value} detail="Running and paused. Archive one to free a slot." /> : null}
					<Text style={styles.note}>{PLAN_USAGE_NOTE}</Text>
				</View>
			) : null}
		</SheetScreen>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	reason: { marginTop: space.sm, fontFamily: "DMSans_400Regular", color: t.textPrimary, fontSize: type.subheadline.fontSize, lineHeight: type.subheadline.lineHeight },
	rows: { marginTop: space.lg },
	loading: { marginTop: space.lg, paddingVertical: space.lg, alignItems: "center", borderTopWidth: rowDividerWidth, borderTopColor: t.borderSubtle },
	note: { marginTop: space.md, fontFamily: "DMSans_400Regular", color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
});
