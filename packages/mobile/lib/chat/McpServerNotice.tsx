import AsyncStorage from "@react-native-async-storage/async-storage";
import { useEffect, useRef, useState } from "react";
import { AccessibilityInfo, Animated, AppState, StyleSheet, Text, View } from "react-native";
import { AppSymbol } from "../AppSymbol";
import type { Theme } from "../theme";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { space, type } from "../tokens";
import { mcpNoticeText } from "./conversationBanners";
import type { McpServer } from "./types";

const NOTICE_MS = 3_000;
const FADE_MS = 200;
const STORAGE_PREFIX = "ao:mcp-notice-shown:";
// The set covers screen remounts; storage keeps the note from replaying after a relaunch.
const shownThisLaunch = new Set<string>();

/**
 * A brief, once-per-session note that some tool servers did not start — the
 * desktop's `McpServerBanner`. The agent never mentions tools it does not have,
 * so without this a reader sees a worse answer with no cause. There is nothing to
 * do from here, so say it once, quietly, above the composer and get out of the
 * way; a restarted session (new `incarnation`) gets its own note. It waits until
 * the conversation is on screen and only counts as said after a full read.
 *
 * Renders as a zero-height strip so it floats over the bottom of the timeline
 * without moving the conversation or the composer.
 */
export function McpServerNotice({ noticeKey, servers, visible }: {
	noticeKey: string;
	servers: readonly McpServer[];
	/**
	 * Whether the chat screen is focused. It stays mounted under pushed routes
	 * and under chat sheets, which are bottom sheets over the composer where the
	 * note sits, so a covered chat must not use up the session's one note — the
	 * desktop's `active` prop.
	 */
	visible: boolean;
}) {
	const [phase, setPhase] = useState<"waiting" | "shown" | "fading" | "done">("waiting");
	const [appActive, setAppActive] = useState(() => AppState.currentState === "active");
	const opacity = useRef(new Animated.Value(1)).current;
	const text = mcpNoticeText(servers);
	const hasFailures = Boolean(text);
	const onScreen = visible && appActive;
	const textRef = useRef(text);
	textRef.current = text;

	useEffect(() => {
		const subscription = AppState.addEventListener("change", (state) => setAppActive(state === "active"));
		return () => subscription.remove();
	}, []);

	useEffect(() => {
		if (phase !== "waiting" || !hasFailures || !onScreen) return;
		if (shownThisLaunch.has(noticeKey)) {
			setPhase("done");
			return;
		}
		let cancelled = false;
		void AsyncStorage.getItem(`${STORAGE_PREFIX}${noticeKey}`)
			.catch(() => null)
			.then((value) => {
				if (cancelled) return;
				if (value === "1") {
					shownThisLaunch.add(noticeKey);
					setPhase("done");
					return;
				}
				setPhase("shown");
			});
		return () => {
			cancelled = true;
		};
	}, [hasFailures, noticeKey, onScreen, phase]);

	useEffect(() => {
		if (phase !== "shown") return;
		// Covered before the reader had the full read: put it back for next time.
		if (!onScreen) {
			setPhase("waiting");
			return;
		}
		// Announce once when the note appears, not again if the list changes under it.
		if (textRef.current) AccessibilityInfo.announceForAccessibility(textRef.current);
		const timer = setTimeout(() => {
			// Used up only once it has been on screen for the whole read.
			shownThisLaunch.add(noticeKey);
			void AsyncStorage.setItem(`${STORAGE_PREFIX}${noticeKey}`, "1").catch(() => {});
			setPhase("fading");
			Animated.timing(opacity, { toValue: 0, duration: FADE_MS, useNativeDriver: true }).start(() => setPhase("done"));
		}, NOTICE_MS);
		return () => clearTimeout(timer);
	}, [noticeKey, onScreen, opacity, phase]);

	if ((phase !== "shown" && phase !== "fading") || !text) return null;
	return <NoticePill text={text} opacity={opacity} />;
}


function NoticePill({ text, opacity }: { text: string; opacity: Animated.Value }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	return (
		<View style={styles.dock} pointerEvents="none">
			<Animated.View style={[styles.pill, { opacity }]}>
				<AppSymbol name="disconnected" size={12} color={t.textTertiary} />
				<Text style={styles.text} numberOfLines={1}>{text}</Text>
			</Animated.View>
		</View>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	dock: { height: 0, zIndex: 1, overflow: "visible" },
	pill: {
		position: "absolute",
		bottom: space.xs,
		alignSelf: "center",
		maxWidth: "92%",
		flexDirection: "row",
		alignItems: "center",
		gap: space.xs,
		paddingHorizontal: space.sm,
		paddingVertical: space.xxs,
		borderRadius: 8,
		borderCurve: "continuous",
		backgroundColor: t.bgBase,
	},
	text: { flexShrink: 1, fontFamily: "DMSans_400Regular", color: t.textTertiary, fontSize: type.caption1.fontSize, lineHeight: type.caption1.lineHeight },
});
