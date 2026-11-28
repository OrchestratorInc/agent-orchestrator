import type { ReactNode } from "react";
import { StyleSheet, View, type LayoutChangeEvent, type StyleProp, type ViewStyle } from "react-native";
import { KeyboardFollower } from "./KeyboardFollower";
import { KeyboardMotionScope } from "./keyboardMotion";
import type { useChatKeyboard } from "./useChatKeyboard";

/**
 * A screen with a composer floating at its foot, held to the keyboard as the
 * chat's is (#8348): the composer rides the keys by a transform on the UI
 * thread (KeyboardFollower), not by the screen's keyboard padding, which is
 * layout and waited on a busy JS thread; and the composer, its follower and
 * everything inside share one keyboard handler (KeyboardMotionScope).
 *
 * The chat, a Cloud session and New task all lay out on it, so a fix to how
 * a composer meets the keyboard reaches each: New task sat in a
 * keyboard-avoiding view and still lagged the keys long after the chat's was
 * smoothed.
 *
 * `keyboard` is the screen's `useChatKeyboard({ floating: true })`. The
 * content is padded for the keyboard only where its progress is not to be
 * trusted (Android's `floatingRootStyle`); otherwise it is the content's own
 * to keep clear of the composer.
 */
export function FloatingComposerLayout({ keyboard, style, content, composer, riding, overlay, onComposerLayout }: {
	keyboard: Pick<ReturnType<typeof useChatKeyboard>, "floatingRootStyle" | "composerLift">;
	style?: StyleProp<ViewStyle>;
	content: ReactNode;
	composer: ReactNode;
	/**
	 * Laid over the whole screen and riding the keys with the composer, for what
	 * floats just above it (New task's "/" list). Only its own views take touches.
	 */
	riding?: ReactNode;
	/** Drawn over the composer, outside its follower (a Cloud session's switch card). */
	overlay?: ReactNode;
	onComposerLayout?: (event: LayoutChangeEvent) => void;
}) {
	return (
		<KeyboardMotionScope>
			<View style={[styles.screen, style]}>
				<View style={[styles.screen, keyboard.floatingRootStyle]}>{content}</View>
				<KeyboardFollower lift={keyboard.composerLift} style={styles.floating} onLayout={onComposerLayout}>{composer}</KeyboardFollower>
				{riding ? <KeyboardFollower lift={keyboard.composerLift} style={StyleSheet.absoluteFill}>{riding}</KeyboardFollower> : null}
				{overlay}
			</View>
		</KeyboardMotionScope>
	);
}

const styles = StyleSheet.create({
	screen: { flex: 1 },
	floating: { position: "absolute", left: 0, right: 0, bottom: 0 },
});
