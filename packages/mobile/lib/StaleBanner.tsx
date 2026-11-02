import { Feather } from "./icons";
import { Pressable, StyleSheet, Text, View } from "react-native";

import { haptics } from "./haptics";
import { describeLinkHealth } from "./linkHealth";
import { staleAgeLabel } from "./screenState";
import { useApp } from "./store";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { fontScaleCap, space, type } from "./tokens";
import { useStaleness } from "./useStaleness";

/**
 * Says out loud that the rows below are older than they look.
 *
 * The screens used to put their failure copy inside `ListEmptyComponent`, which
 * means it only ever appeared when there was nothing to show. A list that had
 * already loaded kept its rows and said nothing at all — so a board whose daemon
 * died an hour ago looked exactly like a live one, and people act on those rows.
 *
 * This component owns the clock rather than taking an age as a prop, and that is
 * deliberate: `useStaleness` re-renders whatever calls it every tick, so a screen
 * that called it would re-render its whole list on a timer. Here the cost is one
 * small component. It also self-gates to null when the data is current, so
 * screens can render it unconditionally.
 *
 * It also says when the phone's own connection is the problem: no network, a
 * link still recovering from a blip, or one that answers slowly. Those are
 * amber and never name the machine as down. `networkOnly` limits it to the
 * phone being offline, for screens that show several machines at once, where
 * one machine's poll is not the whole story.
 */
export function StaleBanner({ error = false, onRetry, networkOnly = false }: { error?: boolean; onRetry?: () => void; networkOnly?: boolean }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const { getLastSyncAt, config, linkHealth, selectedHostName } = useApp();
	const { stale, ageMs } = useStaleness(getLastSyncAt);

	const host = config?.host?.trim();
	const link = describeLinkHealth(linkHealth, selectedHostName ?? host);
	if (networkOnly && linkHealth !== "phone-offline") return null;
	// A machine that answered with an error (a rejected password, a 5xx) keeps
	// its own red copy; "slow" is no explanation for that.
	const showLink = link !== null && !(error && linkHealth === "slow");

	// Fresh and healthy: say nothing. Silence is the correct state here, and it
	// keeps the caller free of a conditional.
	if (!stale && !error && !showLink) return null;

	// A reachability failure is worth more alarm than mere age: one means the
	// desktop is gone, the other can just be a laptop that slept. The phone's own
	// connection is amber: nothing is wrong with the machine.
	const red = error && !showLink;
	const tone = red ? t.red : t.amber;
	const fill = red ? t.tintRed : t.tintAmber;
	const age = staleAgeLabel(ageMs);
	const text = showLink
		? link.text
		: error
			? host
				? `Can't reach ${host} — showing data from ${age}`
				: `Can't reach your machine — showing data from ${age}`
			: `Showing data from ${age}`;
	const icon = showLink ? link.icon : error ? "wifi-off" : "clock";
	// Retrying can't help while the phone itself has no network.
	const retry = linkHealth === "phone-offline" ? undefined : onRetry;

	return (
		<View
			accessibilityRole="alert"
			accessibilityLabel={text}
			style={[styles.banner, { backgroundColor: fill }]}
		>
			<Feather name={icon} size={12} color={tone} />
			<Text
				numberOfLines={1}
				maxFontSizeMultiplier={fontScaleCap.chrome}
				style={styles.bannerText}
			>
				{text}
			</Text>
			{retry ? (
				<Pressable
					accessibilityRole="button"
					accessibilityLabel="Retry now"
					hitSlop={7}
					onPress={() => {
						haptics.tap();
						retry();
					}}
				>
					<Text maxFontSizeMultiplier={fontScaleCap.chrome} style={[styles.bannerAction, { color: tone }]}>
						Retry
					</Text>
				</Pressable>
			) : null}
		</View>
	);
}

// Metrics mirror the chat screen's InlineBanner so the app has one banner
// language rather than two that nearly match.
const makeStyles = (t: Theme) =>
	StyleSheet.create({
		banner: {
			minHeight: 35,
			flexDirection: "row",
			alignItems: "center",
			gap: space.sm,
			paddingHorizontal: space.md,
			paddingVertical: space.xs,
			borderBottomWidth: 1,
			borderBottomColor: t.borderSubtle,
		},
		bannerText: { fontFamily: "Geist_400Regular", flex: 1, color: t.textSecondary, fontSize: type.caption2.fontSize, lineHeight: type.caption2.lineHeight },
		bannerAction: { fontFamily: "Geist_600SemiBold", fontSize: type.caption2.fontSize, lineHeight: type.caption2.lineHeight, fontWeight: "600" },
	});
