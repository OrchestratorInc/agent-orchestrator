import { useEffect, useState } from "react";
import { StyleSheet, Text, View } from "react-native";
import { rowDividerWidth } from "../divider";
import type { Theme } from "../theme";
import { useThemedStyles } from "../ThemeProvider";
import { gutter, radius, space, type } from "../tokens";
import { Button } from "../ui";
import type { ApprovalCard, ApprovalDecision, DecisionResult } from "./types";

type Phase = "idle" | "sending" | "already-decided" | "failed";

/** "1:30 left" until the agent stops waiting. */
function remaining(expiresAt: string, now: number): string {
	const secs = Math.max(0, Math.ceil((Date.parse(expiresAt) - now) / 1000));
	if (Number.isNaN(secs)) return "";
	return `${Math.floor(secs / 60)}:${String(secs % 60).padStart(2, "0")} left`;
}

/**
 * An agent's browser action waiting for the person. The summary, site and
 * reason come from the agent and the page: they are shown as plain text,
 * already capped (approvalCardText). A person who can't decide (a Cloud
 * session someone else owns) sees who decides, with no buttons.
 */
export function BrowserApprovalCard({ card, onDecide }: { card: ApprovalCard; onDecide: (decision: ApprovalDecision) => Promise<DecisionResult> }) {
	const s = useThemedStyles(makeStyles);
	const [now, setNow] = useState(() => Date.now());
	const [phase, setPhase] = useState<Phase>("idle");
	useEffect(() => {
		const timer = setInterval(() => setNow(Date.now()), 1_000);
		return () => clearInterval(timer);
	}, []);
	useEffect(() => setPhase("idle"), [card.id]);

	const decide = async (decision: ApprovalDecision) => {
		setPhase("sending");
		try {
			setPhase((await onDecide(decision)) === "already-decided" ? "already-decided" : "idle");
		} catch {
			setPhase("failed");
		}
	};
	const left = remaining(card.expiresAt, now);
	const meta = [card.site, left].filter(Boolean).join(" · ");

	return <View accessibilityRole="alert" style={s.card}>
		<Text style={s.eyebrow}>Approval needed</Text>
		<Text style={s.summary}>{card.summary}</Text>
		{card.reason ? <Text style={s.reason}>{card.reason}</Text> : null}
		{meta ? <Text style={s.meta}>{meta}</Text> : null}
		{!card.canDecide
			? <Text style={s.status}>{`Waiting for ${card.deciderName || "the session's owner"} to approve`}</Text>
			: phase === "already-decided"
				? <Text style={s.status}>Already decided</Text>
				: <>
					{phase === "failed" ? <Text style={s.error}>Couldn't send your answer. Try again.</Text> : null}
					<View style={s.row}>
						<Button title="Deny" variant="danger" disabled={phase === "sending"} onPress={() => void decide("deny")} style={s.grow} />
						<Button title="Allow once" variant="primary" disabled={phase === "sending"} onPress={() => void decide("allow-once")} style={s.grow} />
					</View>
					{card.canAllowSite ? <Button title={`Always on ${card.site}`} variant="ghost" disabled={phase === "sending"} onPress={() => void decide("allow-site")} /> : null}
				</>}
	</View>;
}

const makeStyles = (t: Theme) => StyleSheet.create({
	card: {
		marginHorizontal: gutter,
		gap: space.xs,
		padding: space.md,
		borderRadius: radius.lg,
		borderCurve: "continuous",
		borderWidth: rowDividerWidth,
		borderColor: t.tintAmber,
		backgroundColor: t.bgElevated,
	},
	eyebrow: { ...type.caption1, color: t.amber },
	summary: { ...type.headline, color: t.textPrimary },
	reason: { ...type.subheadline, color: t.textSecondary },
	meta: { ...type.footnote, color: t.textSecondary },
	status: { ...type.subheadline, color: t.textSecondary, paddingTop: space.xs },
	error: { ...type.footnote, color: t.red },
	row: { flexDirection: "row", gap: space.sm, paddingTop: space.xs },
	grow: { flex: 1 },
});
