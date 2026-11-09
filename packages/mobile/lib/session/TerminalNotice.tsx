import { useEffect } from "react";
import { AccessibilityInfo, StyleSheet, Text, View } from "react-native";
import { AppSymbol } from "../AppSymbol";
import { rowDividerWidth } from "../divider";
import type { Theme } from "../theme";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { font, fontScaleCap, iconSize, space, touchTarget, type } from "../tokens";
import { NoticeDismissButton } from "./NoticeDismissButton";
import type { TerminalNoticeHostProps, TerminalNoticeProps } from "./TerminalNotice.types";

export type { TerminalNoticeHostProps, TerminalNoticeMessage, TerminalNoticeProps, TerminalNoticeTone } from "./TerminalNotice.types";

/**
 * A terminal notice on iOS: an inline row under the navigation bar, led by a
 * symbol that says whether something failed and closed with its own button.
 * iOS has no system snackbar, and a notice here often explains what just
 * happened to the user's text, so it stays until they close it or the next
 * one replaces it. Android shows the same notices as a Material snackbar
 * (./TerminalNotice.android).
 */
export function TerminalNotice({ notice, onDismiss }: TerminalNoticeProps) {
	const t = useTheme();
	const s = useThemedStyles(makeStyles);

	// A row appearing under the bar is silent to VoiceOver otherwise.
	useEffect(() => {
		if (notice) AccessibilityInfo.announceForAccessibility(notice.text);
	}, [notice]);

	if (!notice) return null;
	const error = notice.tone === "error";
	return (
		<View style={[s.notice, error && s.error]} accessibilityRole={error ? "alert" : undefined} testID="terminal-notice">
			<AppSymbol name={error ? "error" : "info"} size={iconSize.md} color={error ? t.red : t.textSecondary} />
			<Text maxFontSizeMultiplier={fontScaleCap.body} style={[s.text, error && s.errorText]}>
				{notice.text}
			</Text>
			<NoticeDismissButton accessibilityLabel="Dismiss" onPress={onDismiss} />
		</View>
	);
}

/** Android's snackbar host. iOS shows notices inline, through `TerminalNotice`. */
export function TerminalNoticeHost(_props: TerminalNoticeHostProps): null {
	return null;
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		notice: {
			flexDirection: "row",
			alignItems: "center",
			gap: space.sm,
			minHeight: touchTarget,
			paddingLeft: space.lg,
			backgroundColor: t.bgElevated,
			borderBottomWidth: rowDividerWidth,
			borderBottomColor: t.borderSubtle,
		},
		error: { backgroundColor: t.tintRed },
		text: {
			flex: 1,
			paddingVertical: space.sm,
			fontFamily: font.regular,
			fontSize: type.footnote.fontSize,
			lineHeight: type.footnote.lineHeight,
			color: t.textPrimary,
		},
		errorText: { color: t.red },
	});
