import { useCallback } from "react";
import { FlatList, Pressable, StyleSheet, Text, View, type AccessibilityActionEvent, type LayoutChangeEvent } from "react-native";
import { Gesture, GestureDetector } from "react-native-gesture-handler";
import Animated, { Easing, useAnimatedStyle, useSharedValue, withSpring, withTiming } from "react-native-reanimated";
import { scheduleOnRN } from "react-native-worklets";
import { AppSymbol } from "../AppSymbol";
import { rowDividerWidth } from "../divider";
import { haptics } from "../haptics";
import { usePressFeedback } from "../pressFeedback";
import type { Theme } from "../theme";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { duration, fontScaleCap, gutter, iconSize, space, spring, touchTarget, type } from "../tokens";
import { useReducedMotion } from "../useReducedMotion";
import { queuedCommentLabel, type QueuedCommentsProps } from "./QueuedComments.types";
import type { ReviewComment } from "./reviewStore";

export type { QueuedCommentsProps } from "./QueuedComments.types";

/** How far left a row has to travel before letting go removes it. */
const COMMIT_FRACTION = 0.4;
const FLING_VELOCITY = 1000;

/**
 * The queued comments as Material list rows. A row swipes away to the left
 * to remove it, with a bump when it passes the point of no return; the trailing
 * delete button and TalkBack's action do the same without the gesture.
 */
export function QueuedComments({ comments, onRemove, disabled = false }: QueuedCommentsProps) {
	const styles = useThemedStyles(makeStyles);
	return (
		<FlatList
			data={comments}
			keyExtractor={(comment) => comment.id}
			style={styles.list}
			contentContainerStyle={styles.content}
			ItemSeparatorComponent={() => <View style={styles.divider} />}
			renderItem={({ item }) => <QueuedCommentRow comment={item} onRemove={onRemove} disabled={disabled} />}
		/>
	);
}

function QueuedCommentRow({ comment, onRemove, disabled }: { comment: ReviewComment; onRemove: (id: string) => void; disabled: boolean }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const feedback = usePressFeedback("icon", { tone: "destructive" });
	const reduceMotion = useReducedMotion();
	const width = useSharedValue(0);
	const start = useSharedValue(0);
	const x = useSharedValue(0);
	const armed = useSharedValue(false);
	const committed = useSharedValue(false);

	const remove = useCallback(() => onRemove(comment.id), [comment.id, onRemove]);
	const arm = useCallback(() => haptics.armed(), []);
	const onLayout = useCallback((event: LayoutChangeEvent) => { width.value = event.nativeEvent.layout.width; }, [width]);

	const pan = Gesture.Pan()
		.enabled(!disabled)
		.activeOffsetX([-16, 16])
		.failOffsetY([-12, 12])
		.onBegin(() => {
			start.value = x.value;
		})
		.onUpdate((event) => {
			x.value = Math.min(0, start.value + event.translationX);
			const past = -x.value > width.value * COMMIT_FRACTION;
			if (past !== armed.value) {
				armed.value = past;
				if (past) scheduleOnRN(arm);
			}
		})
		.onEnd((event) => {
			if (!committed.value && (armed.value || event.velocityX < -FLING_VELOCITY)) {
				committed.value = true;
				x.value = withTiming(-width.value, { duration: reduceMotion ? 0 : duration.slow, easing: Easing.out(Easing.cubic) }, (finished) => {
					if (finished) scheduleOnRN(remove);
				});
				return;
			}
			armed.value = false;
			x.value = reduceMotion ? 0 : withSpring(0, spring.snappy);
		});

	const rowStyle = useAnimatedStyle(() => ({ transform: [{ translateX: x.value }] }));
	const onAccessibilityAction = (event: AccessibilityActionEvent) => {
		if (event.nativeEvent.actionName === "delete" && !disabled) remove();
	};

	return (
		<View onLayout={onLayout} style={styles.shell}>
			<View pointerEvents="none" style={styles.rail}>
				<AppSymbol name="delete" color={t.red} />
			</View>
			<GestureDetector gesture={pan}>
				<Animated.View style={[styles.row, rowStyle]}>
					<View
						accessible
						accessibilityLabel={queuedCommentLabel(comment)}
						accessibilityActions={[{ name: "delete", label: "Remove comment" }]}
						onAccessibilityAction={onAccessibilityAction}
						style={styles.text}
					>
						<Text maxFontSizeMultiplier={fontScaleCap.chrome} numberOfLines={1} ellipsizeMode="middle" style={styles.label}>{comment.label}</Text>
						<Text maxFontSizeMultiplier={fontScaleCap.body} style={styles.feedback}>{comment.feedback}</Text>
					</View>
					<Pressable
						accessibilityRole="button"
						accessibilityLabel={`Remove comment on ${comment.label}`}
						disabled={disabled}
						onPress={remove}
						android_ripple={feedback.android_ripple}
						style={styles.remove}
					>
						<AppSymbol name="delete" color={t.textTertiary} size={iconSize.xl} />
					</Pressable>
				</Animated.View>
			</GestureDetector>
		</View>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	list: { flex: 1 },
	content: { paddingBottom: space.lg },
	shell: { overflow: "hidden" },
	rail: { position: "absolute", top: 0, right: 0, bottom: 0, left: 0, alignItems: "flex-end", justifyContent: "center", paddingRight: gutter + space.md, backgroundColor: t.tintRed },
	row: { flexDirection: "row", alignItems: "center", gap: space.sm, paddingLeft: gutter, paddingRight: space.xs, paddingVertical: space.md, backgroundColor: t.bgSurface },
	text: { flex: 1, minWidth: 0, gap: space.xxs },
	label: { fontFamily: t.fontMono, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, color: t.textTertiary },
	feedback: { ...type.subheadline, color: t.textPrimary },
	remove: { width: touchTarget, height: touchTarget, alignItems: "center", justifyContent: "center" },
	divider: { height: rowDividerWidth, backgroundColor: t.borderSubtle, marginLeft: gutter },
});
