import { Pressable, StyleSheet, Text, View } from "react-native";

import { FreshnessDot } from "./FreshnessDot";
import { haptics } from "./haptics";
import { freshnessColor, freshnessLine, machineFreshness } from "./machineFreshness";
import { useApp } from "./store";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { font, fontScaleCap, gutter, space, type } from "./tokens";
import { FRESHNESS_TICK_MS } from "./useMachineFreshness";
import { useNow } from "./useNow";

/**
 * One quiet line under the page title when its rows may not be current.
 *
 * Replaces the full-width banner that sat above the board. It says something
 * in two cases only: the phone itself is offline, which affects every machine,
 * and a page whose rows are all on one machine (`hostId`) when that machine is
 * lagging or can't be reached. A page showing several machines passes no
 * `hostId`, and their dots on the machine chips and rows say it instead.
 *
 * Owns its clock, like the banner did, so only this line redraws as the age
 * ticks over; it renders nothing at all while the data is current.
 */
export function FreshnessLine({ hostId, onRetry }: { hostId?: string; onRetry?: () => void }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const app = useApp();
	const now = useNow(FRESHNESS_TICK_MS);

	const host = hostId ? app.hostStates.find((item) => item.hostId === hostId) : undefined;
	// The selected machine's snapshot trails its polls (see readFleetFreshness).
	const syncedAt = (id: string, fallback: number) => (id === app.currentHostId ? app.getLastSyncAt() : fallback);
	const lastSyncAt = host
		? syncedAt(host.hostId, host.lastSyncAt)
		: Math.max(0, ...app.hostStates.map((item) => syncedAt(item.hostId, item.lastSyncAt)));
	const phoneOffline = app.phoneOffline || app.linkHealth === "phone-offline";
	const line = freshnessLine({
		machine: host?.name,
		freshness: host ? machineFreshness({ ...host, lastSyncAt }, app.phoneNetwork, now) : undefined,
		lastSyncAt,
		phoneOffline,
		now,
	});
	if (!line) return null;

	const color = freshnessColor(t, line.tone);
	return (
		<View accessibilityRole="alert" accessibilityLabel={line.text} style={styles.line} testID="freshness-line">
			<FreshnessDot tone={line.tone} />
			{/* Polite: TalkBack announces a change (lagging to unreachable) without
			    moving focus; the alert role alone does not. Retry stays its own button. */}
			<Text accessibilityLiveRegion="polite" numberOfLines={1} maxFontSizeMultiplier={fontScaleCap.chrome} style={styles.text}>
				{line.text}
			</Text>
			{line.retry && onRetry ? (
				<Pressable
					accessibilityRole="button"
					accessibilityLabel="Retry now"
					hitSlop={12}
					onPress={() => {
						haptics.tap();
						onRetry();
					}}
				>
					<Text maxFontSizeMultiplier={fontScaleCap.chrome} style={[styles.retry, { color }]}>Retry</Text>
				</Pressable>
			) : null}
		</View>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		line: { flexDirection: "row", alignItems: "center", gap: space.xs + 2, paddingHorizontal: gutter, paddingBottom: space.sm },
		text: { ...type.footnote, flexShrink: 1, color: t.textSecondary },
		retry: { ...type.footnote, fontFamily: font.semibold, fontWeight: "600" },
	});
