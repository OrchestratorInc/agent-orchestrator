import { useEffect, useRef, useState, type ReactNode } from "react";
import { Animated, Easing, Modal, Pressable, StyleSheet, Text, View, useWindowDimensions, type StyleProp, type ViewStyle } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { haptics } from "../haptics";
import { Feather } from "../icons";
import { CROSSFADE_MS } from "../motion";
import type { Theme } from "../theme";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { space, type } from "../tokens";
import { useReducedMotion } from "../useReducedMotion";

export type MessageMenuAction = "edit" | "copy";

type Anchor = { x: number; y: number; width: number; height: number };

const MENU_WIDTH = 220;
const ROW_HEIGHT = 48;
const GAP = 8;

/**
 * Long-press menu on a sent human message: Edit (when the daemon can branch
 * before it) and Copy, as the desktop shows them on hover.
 *
 * Android's native popup is a small Material list that cannot be styled to sit
 * beside iOS's context menu, so this draws the same interaction: the screen
 * dims, the bubble lifts in place, and a rounded card of actions opens under it.
 * iOS keeps the system menu (MessageContextMenu.ios.tsx).
 */
export function MessageContextMenu({ children, canEdit, onAction, style }: {
	children: ReactNode;
	canEdit: boolean;
	onAction(action: MessageMenuAction): void;
	style?: StyleProp<ViewStyle>;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const reduceMotion = useReducedMotion();
	const window = useWindowDimensions();
	const insets = useSafeAreaInsets();
	const target = useRef<View>(null);
	const [anchor, setAnchor] = useState<Anchor>();
	const progress = useRef(new Animated.Value(0)).current;
	const actions: { id: MessageMenuAction; title: string; icon: "edit-2" | "copy" }[] = [
		...(canEdit ? [{ id: "edit" as const, title: "Edit", icon: "edit-2" as const }] : []),
		{ id: "copy", title: "Copy", icon: "copy" },
	];

	useEffect(() => {
		if (!anchor) return;
		progress.setValue(reduceMotion ? 1 : 0);
		if (!reduceMotion) Animated.timing(progress, { toValue: 1, duration: CROSSFADE_MS + 40, easing: Easing.out(Easing.cubic), useNativeDriver: true }).start();
	}, [anchor, progress, reduceMotion]);

	const open = () => {
		target.current?.measureInWindow((x, y, width, height) => {
			if (!width || !height) return;
			haptics.press();
			setAnchor({ x, y, width, height });
		});
	};
	const close = () => setAnchor(undefined);
	const choose = (action: MessageMenuAction) => {
		close();
		onAction(action);
	};

	// Under the bubble when it fits, above it otherwise, right-aligned with it
	// the way the bubble itself sits.
	const menuHeight = actions.length * ROW_HEIGHT;
	const placement = anchor ? (() => {
		const below = anchor.y + anchor.height + GAP;
		const fitsBelow = below + menuHeight <= window.height - insets.bottom - space.md;
		const top = fitsBelow ? below : Math.max(insets.top + space.md, anchor.y - GAP - menuHeight);
		const right = Math.min(anchor.x + anchor.width, window.width - space.md);
		const left = Math.max(space.md, right - MENU_WIDTH);
		return { top, left, origin: fitsBelow ? -1 : 1 };
	})() : undefined;

	return (
		<View style={style}>
			<Pressable ref={target} onLongPress={open} delayLongPress={350} accessibilityHint="Long-press for Edit and Copy" accessibilityActions={[...(canEdit ? [{ name: "edit", label: "Edit" }] : []), { name: "copy", label: "Copy" }]} onAccessibilityAction={(event) => onAction(event.nativeEvent.actionName as MessageMenuAction)}>
				{children}
			</Pressable>
			<Modal visible={Boolean(anchor)} transparent animationType="none" statusBarTranslucent navigationBarTranslucent onRequestClose={close}>
				{anchor && placement ? <>
					<Animated.View style={[StyleSheet.absoluteFill, styles.scrim, { opacity: progress }]}>
						<Pressable accessibilityLabel="Close menu" style={StyleSheet.absoluteFill} onPress={close} />
					</Animated.View>
					{/* The bubble itself, lifted above the dimmed conversation. */}
					<Animated.View pointerEvents="none" style={[styles.preview, { top: anchor.y, left: anchor.x, width: anchor.width, transform: [{ scale: progress.interpolate({ inputRange: [0, 1], outputRange: [1, 1.03] }) }] }]}>
						{children}
					</Animated.View>
					<Animated.View style={[styles.menu, { top: placement.top, left: placement.left, opacity: progress, transform: [
						{ translateY: progress.interpolate({ inputRange: [0, 1], outputRange: [placement.origin * -12, 0] }) },
						{ scale: progress.interpolate({ inputRange: [0, 1], outputRange: [0.92, 1] }) },
					] }]}>
						{actions.map((action, index) => (
							<Pressable
								key={action.id}
								accessibilityRole="menuitem"
								accessibilityLabel={action.title}
								onPress={() => { haptics.tap(); choose(action.id); }}
								android_ripple={{ color: t.bgElevatedHover }}
								style={({ pressed }) => [styles.row, index > 0 && styles.divided, pressed && { backgroundColor: t.bgElevatedHover }]}
							>
								<Text style={styles.title}>{action.title}</Text>
								<Feather name={action.icon} size={17} color={t.textPrimary} />
							</Pressable>
						))}
					</Animated.View>
				</> : null}
			</Modal>
		</View>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	scrim: { backgroundColor: t.scrim },
	preview: { position: "absolute", alignItems: "flex-end", elevation: 12, shadowColor: "#000", shadowOpacity: 0.25, shadowRadius: 16, shadowOffset: { width: 0, height: 6 } },
	menu: { position: "absolute", width: MENU_WIDTH, borderRadius: 16, borderCurve: "continuous", overflow: "hidden", backgroundColor: t.bgElevated, borderWidth: StyleSheet.hairlineWidth, borderColor: t.borderDefault, elevation: 16, shadowColor: "#000", shadowOpacity: 0.3, shadowRadius: 20, shadowOffset: { width: 0, height: 8 } },
	row: { height: ROW_HEIGHT, flexDirection: "row", alignItems: "center", justifyContent: "space-between", paddingHorizontal: space.lg },
	divided: { borderTopWidth: StyleSheet.hairlineWidth, borderTopColor: t.borderSubtle },
	title: { fontFamily: "Geist_400Regular", color: t.textPrimary, fontSize: type.body.fontSize, lineHeight: type.body.lineHeight },
});
