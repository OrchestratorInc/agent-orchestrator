import { useState } from "react";
import { Pressable, StyleSheet, Text, TextInput, View } from "react-native";
import { KeyboardStickyView } from "react-native-keyboard-controller";
import { replyQuote } from "../../shared/review/review-messages";
import { AppSymbol } from "./AppSymbol";
import { haptics } from "./haptics";
import { usePressFeedback } from "./pressFeedback";
import { commentReplyLabel } from "./reviewCommentView";
import type { ReviewReplyTarget } from "./ReviewCommentsSection";
import { Spinner } from "./Spinner";
import { textSelectionColors } from "./textSelection";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { fontScaleCap, iconSize, radius, space, touchTarget, type } from "./tokens";

/**
 * Answers a review comment on GitHub, docked over the keyboard where the PR
 * page keeps its merge bar. A thread takes the reply in place; a review or a
 * top-level comment is answered by quoting it, as the desktop does, so the
 * reply nests under it on GitHub.
 */
export function ReviewReplyDock({
	target,
	bottomInset,
	onSend,
	onClose,
}: {
	target: ReviewReplyTarget;
	bottomInset: number;
	/** Posts the reply; rejects with what went wrong. */
	onSend: (body: string) => Promise<void>;
	onClose: () => void;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const { comment } = target;
	const [text, setText] = useState(() => comment.kind === "inline" ? "" : replyQuote(comment.author, comment.body));
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState("");
	const closeFeedback = usePressFeedback("icon");
	const sendFeedback = usePressFeedback("button", { tone: "onAccent" });
	const trimmed = text.trim();
	const label = commentReplyLabel(comment);
	const send = async () => {
		if (!trimmed || busy) return;
		setBusy(true);
		setError("");
		try {
			await onSend(trimmed);
			haptics.success();
		} catch (cause) {
			setError(cause instanceof Error ? cause.message : "Could not post the reply.");
		} finally {
			setBusy(false);
		}
	};
	return (
		<KeyboardStickyView offset={{ closed: 0, opened: bottomInset - space.sm }}>
			<View style={[styles.dock, { paddingBottom: Math.max(bottomInset, space.md) }]}>
				<View style={styles.meta}>
					<AppSymbol name="chat" size={iconSize.sm} color={t.textTertiary} />
					<Text accessibilityRole="header" maxFontSizeMultiplier={fontScaleCap.chrome} numberOfLines={1} style={styles.label}>{label}</Text>
					<Pressable accessibilityRole="button" accessibilityLabel="Cancel reply" onPress={onClose} android_ripple={closeFeedback.android_ripple} style={({ pressed }) => [styles.icon, pressed && closeFeedback.pressed]}>
						<AppSymbol name="close" size={iconSize.md} color={t.textSecondary} />
					</Pressable>
				</View>
				{error ? <Text accessibilityRole="alert" style={styles.error}>{error}</Text> : null}
				<View style={styles.field}>
					<TextInput
						value={text}
						onChangeText={(next) => { setText(next); if (error) setError(""); }}
						multiline
						autoFocus
						editable={!busy}
						placeholder="Write a reply…"
						{...textSelectionColors(t)}
						placeholderTextColor={t.textFaint}
						accessibilityLabel={label}
						style={styles.input}
						maxFontSizeMultiplier={fontScaleCap.body}
					/>
					<Pressable
						accessibilityRole="button"
						accessibilityLabel="Post reply"
						accessibilityState={{ disabled: !trimmed || busy, busy }}
						disabled={!trimmed || busy}
						onPress={() => void send()}
						android_ripple={sendFeedback.android_ripple}
						style={({ pressed }) => [styles.send, !trimmed && !busy && styles.disabled, pressed && sendFeedback.pressed]}
					>
						{busy ? <Spinner color={t.onAccent} /> : <AppSymbol name="send" size={iconSize.md} color={t.onAccent} weight="semibold" />}
					</Pressable>
				</View>
			</View>
		</KeyboardStickyView>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	dock: { paddingHorizontal: space.lg, paddingTop: space.sm, gap: space.xs, backgroundColor: t.bgBase, borderTopWidth: StyleSheet.hairlineWidth, borderTopColor: t.borderDefault },
	meta: { flexDirection: "row", alignItems: "center", gap: space.xs },
	label: { flex: 1, fontFamily: "DMSans_500Medium", fontWeight: "500", color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
	icon: { width: touchTarget, height: touchTarget, marginVertical: -space.sm, marginRight: -space.sm, alignItems: "center", justifyContent: "center" },
	error: { fontFamily: "DMSans_400Regular", color: t.red, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
	field: { flexDirection: "row", alignItems: "flex-end", gap: space.sm, paddingLeft: space.md, paddingRight: space.xxs, paddingVertical: space.xxs, borderRadius: radius.xl, borderWidth: StyleSheet.hairlineWidth, borderColor: t.borderStrong, backgroundColor: t.bgSurface },
	input: { flex: 1, maxHeight: 160, paddingVertical: space.xs, fontFamily: "DMSans_400Regular", color: t.textPrimary, fontSize: type.body.fontSize, lineHeight: type.body.lineHeight },
	send: { width: 36, height: 36, borderRadius: 18, alignItems: "center", justifyContent: "center", backgroundColor: t.accent },
	disabled: { opacity: 0.4 },
});
