import type { CloudApiError } from "@aoagents/cloud-client";
import * as Clipboard from "expo-clipboard";
import { useEffect, useRef, useState } from "react";
import { Pressable, StyleSheet, Text, View } from "react-native";
import { Feather } from "../icons";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import type { Theme } from "../theme";
import { fontScaleCap, iconSize, space, touchTarget, type } from "../tokens";
import { Button } from "../ui";

/** A confirmed rejection, not an unknown outcome or a duplicate-worker warning. */
export function CloudSessionLimitNotice({ error, onRetry, busy }: { error: CloudApiError; onRetry(): void; busy: boolean }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const [expanded, setExpanded] = useState(false);
	const [copied, setCopied] = useState(false);
	const [copyError, setCopyError] = useState(false);
	const current = useRef(error); current.current = error;
	const mounted = useRef(true);
	useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
	useEffect(() => { setExpanded(false); setCopied(false); setCopyError(false); }, [error]);
	const copySupportInfo = async () => {
		try {
			await Clipboard.setStringAsync(JSON.stringify({
				status: error.status,
				code: error.code,
				...(error.requestId ? { requestId: error.requestId } : {}),
			}));
			if (mounted.current && current.current === error) { setCopied(true); setCopyError(false); }
		} catch { if (mounted.current && current.current === error) { setCopied(false); setCopyError(true); } }
	};
	return <View style={styles.notice}>
		<View accessibilityRole="alert" accessibilityLiveRegion="polite" style={styles.message}>
			<Feather name="alert-circle" size={iconSize.md} color={t.amber} />
			<View style={styles.copy}>
				<Text maxFontSizeMultiplier={fontScaleCap.body} style={styles.title}>Cloud session limit reached</Text>
				<Text selectable maxFontSizeMultiplier={fontScaleCap.body} style={styles.body}>This worker wasn’t created. Archive an unused Cloud session, then try again.</Text>
			</View>
		</View>
		<View style={styles.actions}>
			<Button title="Try again" variant="text" disabled={busy} onPress={() => { if (!busy) onRetry(); }} />
			<Pressable accessibilityRole="button" accessibilityLabel="Details" accessibilityState={{ expanded }} onPress={() => setExpanded((value) => !value)} style={styles.disclosure}>
				<Text maxFontSizeMultiplier={fontScaleCap.chrome} style={styles.detailsLabel}>Details</Text>
				<Feather name={expanded ? "chevron-up" : "chevron-down"} size={iconSize.xs} color={t.textSecondary} />
			</Pressable>
		</View>
		{expanded ? <View style={styles.details}>
			<Text selectable maxFontSizeMultiplier={fontScaleCap.body} style={styles.body}>Your organization’s Cloud session limit is shared across all projects. Paused sessions still count.</Text>
			<Text selectable maxFontSizeMultiplier={fontScaleCap.body} style={styles.body}>Use Manage Cloud sessions to archive an unused session. Cloud cleanup may take a moment.</Text>
			<Button title={copied ? "Support info copied" : "Copy support info"} variant="text" onPress={() => { void copySupportInfo(); }} />
			{copyError ? <Text accessibilityRole="alert" maxFontSizeMultiplier={fontScaleCap.body} style={styles.body}>Couldn’t copy support info. Try again.</Text> : null}
		</View> : null}
	</View>;
}

const makeStyles = (t: Theme) => StyleSheet.create({
	notice: { gap: space.xs },
	message: { flexDirection: "row", alignItems: "flex-start", gap: space.sm },
	copy: { flex: 1, gap: space.xs },
	title: { ...type.subheadline, fontFamily: "Geist_600SemiBold", fontWeight: "600", color: t.textPrimary },
	body: { ...type.footnote, color: t.textSecondary },
	actions: { flexDirection: "row", alignItems: "center", gap: space.lg },
	disclosure: { minHeight: touchTarget, flexDirection: "row", alignItems: "center", gap: space.xs },
	detailsLabel: { ...type.footnote, color: t.textSecondary },
	details: { gap: space.xs, paddingLeft: iconSize.md + space.sm },
});
