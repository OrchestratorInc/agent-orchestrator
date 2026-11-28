import { createContext, useContext, useEffect, useState } from "react";
import { AppState, StyleSheet, Text, View } from "react-native";
import { rowDividerWidth } from "../divider";
import { Feather } from "../icons";
import { iconSize, radius, space, type } from "../tokens";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import type { Theme } from "../theme";
import { Button } from "../ui";
import type { ConversationTurn } from "./types";
import { accountHandoffCopy, usageLimitNoticeView, usageLimitResetAt, type AccountHandoffNote } from "./usageLimit";

/** What the timeline needs from its screen to recover a usage-limited turn. */
export type TurnRecovery = {
	/** The one turn whose notice offers Resume and auto-resume (see usageLimitedTurn). */
	limitedTurnId?: string;
	resume(turnId: string): Promise<void>;
	setAutoResume(turnId: string, enabled: boolean): Promise<void>;
	/** The name of a signed-in agent account, or undefined when it is unknown. */
	accountLabel(accountId: string | undefined): string | undefined;
};

export const TurnRecoveryContext = createContext<TurnRecovery | null>(null);

export function useTurnRecovery(): TurnRecovery | null {
	return useContext(TurnRecoveryContext);
}

/** Re-renders when the reset passes, including after the app was in the background. */
function useNowUntil(deadline: number | undefined): number {
	const [now, setNow] = useState(Date.now);
	const pending = deadline !== undefined && now < deadline;
	useEffect(() => {
		if (!pending) return;
		const update = () => setNow(Date.now());
		// Timers stall while the app is suspended; coming back re-reads the clock.
		const timer = setTimeout(update, Math.min(deadline - Date.now(), 2_147_483_647));
		const subscription = AppState.addEventListener("change", (state) => { if (state === "active") update(); });
		return () => { clearTimeout(timer); subscription.remove(); };
		// `now` re-arms the wait when a timer ends early (the clock moved back).
	}, [deadline, pending, now]);
	return now;
}

/**
 * A turn the provider refused for a usage limit. The actionable one offers
 * auto-resume at the reset, which the daemon carries out whether or not the
 * phone is open, or Resume when the reset time is unknown.
 */
export function UsageLimitNotice({ turn }: { turn: ConversationTurn & { usageLimit: NonNullable<ConversationTurn["usageLimit"]> } }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const recovery = useTurnRecovery();
	const now = useNowUntil(usageLimitResetAt(turn.usageLimit));
	const [pending, setPending] = useState<"resume" | "autoResume">();
	const [error, setError] = useState<string>();
	const actionable = Boolean(recovery && recovery.limitedTurnId === turn.id);
	const view = usageLimitNoticeView(turn.usageLimit, now);
	const shownError = actionable ? error ?? turn.usageLimit.resumeError : undefined;

	const run = (kind: "resume" | "autoResume", action: () => Promise<void>) => {
		if (pending) return;
		setPending(kind);
		setError(undefined);
		action()
			.catch((cause: unknown) => setError(cause instanceof Error ? cause.message : String(cause)))
			.finally(() => setPending(undefined));
	};

	return (
		<View testID="usage-limit-notice" style={styles.notice}>
			<Feather name="clock" size={iconSize.sm} color={t.amber} style={styles.icon} />
			<View style={styles.body}>
				<Text style={styles.title}>Usage limit reached</Text>
				{turn.errorMessage ? <Text selectable style={styles.detail}>{turn.errorMessage}</Text> : null}
				{actionable && view.status ? <Text accessibilityRole="text" style={styles.status}>{view.status}</Text> : null}
				{actionable && recovery && (view.showResume || view.showAutoResume) ? (
					<View style={styles.actions}>
						{view.showResume ? (
							<Button
								title="Resume"
								icon="play"
								loading={pending === "resume"}
								disabled={Boolean(pending)}
								onPress={() => run("resume", () => recovery.resume(turn.id))}
							/>
						) : null}
						{view.showAutoResume ? (
							<Button
								title={view.scheduled ? "Cancel auto-resume" : "Auto-resume at reset"}
								icon={view.scheduled ? undefined : "timer"}
								variant={view.scheduled ? "ghost" : "secondary"}
								loading={pending === "autoResume"}
								disabled={Boolean(pending)}
								onPress={() => run("autoResume", () => recovery.setAutoResume(turn.id, !view.scheduled))}
							/>
						) : null}
					</View>
				) : null}
				{shownError ? <Text accessibilityRole="alert" style={styles.error}>{shownError}</Text> : null}
			</View>
		</View>
	);
}

/** The daemon moved a usage-limited session to another signed-in account. */
export function AccountHandoffRow({ note }: { note: AccountHandoffNote }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const recovery = useTurnRecovery();
	const copy = accountHandoffCopy(note, recovery?.accountLabel(note.accountId));
	return (
		<View testID="account-handoff-row" style={styles.handoff}>
			<Feather name="shuffle" size={iconSize.sm} color={t.textTertiary} style={styles.icon} />
			<View style={styles.body}>
				<Text style={styles.title}>{copy.title}</Text>
				{copy.detail ? <Text style={styles.detail}>{copy.detail}</Text> : null}
			</View>
		</View>
	);
}

// The timeline's card shape (see ChatTimeline's makeStyles).
const card = { borderRadius: radius.md, borderCurve: "continuous", borderWidth: rowDividerWidth } as const;

const makeStyles = (t: Theme) => StyleSheet.create({
	notice: { ...card, flexDirection: "row", alignItems: "flex-start", gap: space.sm, borderColor: t.borderDefault, backgroundColor: t.bgSurface, padding: space.md },
	handoff: { ...card, marginVertical: space.xs, flexDirection: "row", alignItems: "flex-start", gap: space.sm, borderColor: t.borderDefault, backgroundColor: t.bgSurface, padding: space.sm },
	icon: { marginTop: space.hair },
	body: { flex: 1, minWidth: 0, gap: space.xxs },
	title: { fontFamily: "DMSans_600SemiBold", color: t.textPrimary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, fontWeight: "600" },
	detail: { fontFamily: "DMSans_400Regular", color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
	status: { fontFamily: "DMSans_500Medium", color: t.textTertiary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, fontWeight: "500" },
	actions: { flexDirection: "row", flexWrap: "wrap", gap: space.sm, marginTop: space.xs },
	error: { fontFamily: "DMSans_400Regular", color: t.red, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
});
