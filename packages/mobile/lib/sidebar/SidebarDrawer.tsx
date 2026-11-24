import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { BackHandler, Pressable, StyleSheet, View, useWindowDimensions } from "react-native";
import { Gesture, GestureDetector } from "react-native-gesture-handler";
import Animated, { Easing, interpolate, useAnimatedStyle, useSharedValue, withTiming } from "react-native-reanimated";
import { scheduleOnRN } from "react-native-worklets";
import { haptics } from "../haptics";
import { useReducedMotion } from "../useReducedMotion";
import { useTheme } from "../ThemeProvider";
import { SidebarContext } from "./sidebarContext";

/**
 * DESIGN.md's mobile overlay width for the sidebar. A phone narrower than that
 * plus a strip of the page keeps the strip, so the page under it stays visible
 * and tappable as the way out.
 */
export const SIDEBAR_OVERLAY_WIDTH = 288;
const PAGE_PEEK = 56;
/** The strip along the left edge a swipe opens the sidebar from. */
const EDGE_WIDTH = 24;
/** The shell's ~280ms settle (DESIGN.md §10). */
const SETTLE_MS = 280;

/**
 * The desktop's left sidebar, presented the way a phone expects: a drawer that
 * slides in from the left edge over the page, opened by the page's sidebar
 * button or a swipe from the edge, and closed by a tap on the page, a swipe
 * back, the system back, or choosing somewhere to go.
 *
 * The left-edge swipe belongs to the root page only: on a pushed page iOS's own
 * edge swipe goes back, and taking it would break that. Pushed pages open the
 * sidebar from their header button instead.
 */
export function SidebarDrawer({ children, sidebar }: { children: ReactNode; sidebar: (close: () => void) => ReactNode }) {
	const t = useTheme();
	const window = useWindowDimensions();
	const reduceMotion = useReducedMotion();
	const width = Math.min(SIDEBAR_OVERLAY_WIDTH, window.width - PAGE_PEEK);
	const [isOpen, setIsOpen] = useState(false);
	// Mounted while open or animating; unmounted at rest so a closed drawer costs nothing.
	const [mounted, setMounted] = useState(false);
	const [edgeClaims, setEdgeClaims] = useState(0);
	const progress = useSharedValue(0);
	const dragging = useSharedValue(false);

	const settle = useCallback((to: 0 | 1) => {
		"worklet";
		progress.value = withTiming(to, { duration: reduceMotion ? 0 : SETTLE_MS, easing: Easing.out(Easing.cubic) }, (finished) => {
			if (finished && to === 0) scheduleOnRN(setMounted, false);
		});
	}, [progress, reduceMotion]);

	const open = useCallback(() => {
		setMounted(true);
		setIsOpen(true);
		settle(1);
	}, [settle]);
	const close = useCallback(() => {
		setIsOpen(false);
		settle(0);
	}, [settle]);
	const toggle = useCallback(() => (isOpen ? close() : open()), [close, isOpen, open]);
	const claimEdgeSwipe = useCallback(() => {
		setEdgeClaims((count) => count + 1);
		return () => setEdgeClaims((count) => count - 1);
	}, []);

	// Android's back closes the sidebar before it leaves the page.
	useEffect(() => {
		if (!isOpen) return;
		const subscription = BackHandler.addEventListener("hardwareBackPress", () => {
			close();
			return true;
		});
		return () => subscription.remove();
	}, [close, isOpen]);

	const finishDrag = useCallback((opened: boolean) => {
		if (opened) {
			haptics.select();
			setIsOpen(true);
		} else {
			setIsOpen(false);
		}
	}, []);

	// From the left edge: the drawer follows the finger and settles on release.
	const edgePan = useMemo(() => Gesture.Pan()
		.activeOffsetX(12)
		.failOffsetY([-20, 20])
		.onBegin(() => {
			scheduleOnRN(setMounted, true);
		})
		.onStart(() => {
			dragging.value = true;
		})
		.onUpdate((event) => {
			progress.value = Math.min(1, Math.max(0, event.translationX / width));
		})
		.onEnd((event) => {
			dragging.value = false;
			const opened = event.velocityX > 400 || (event.velocityX > -400 && progress.value > 0.4);
			settle(opened ? 1 : 0);
			scheduleOnRN(finishDrag, opened);
		})
		.onFinalize(() => {
			if (!dragging.value && progress.value === 0) scheduleOnRN(setMounted, false);
		}), [dragging, finishDrag, progress, settle, width]);

	// On the open drawer: a swipe left closes it. Vertical drags stay with its list.
	const panelPan = useMemo(() => Gesture.Pan()
		.activeOffsetX(-12)
		.failOffsetY([-12, 12])
		.onUpdate((event) => {
			progress.value = Math.min(1, Math.max(0, 1 + event.translationX / width));
		})
		.onEnd((event) => {
			const opened = !(event.velocityX < -400 || (event.velocityX < 400 && progress.value < 0.6));
			settle(opened ? 1 : 0);
			scheduleOnRN(finishDrag, opened);
		}), [finishDrag, progress, settle, width]);

	const panelStyle = useAnimatedStyle(() => ({
		transform: [{ translateX: interpolate(progress.value, [0, 1], [-width, 0]) }],
	}));
	const scrimStyle = useAnimatedStyle(() => ({ opacity: progress.value }));

	const value = useMemo(() => ({ isOpen, open, close, toggle, claimEdgeSwipe }), [claimEdgeSwipe, close, isOpen, open, toggle]);
	const closeRef = useRef(close);
	closeRef.current = close;
	const content = useMemo(() => sidebar(() => closeRef.current()), [sidebar]);

	return (
		<SidebarContext.Provider value={value}>
			<View style={styles.fill}>
				{/* The page under the drawer is hidden from a screen reader while it is open. */}
				<View style={styles.fill} importantForAccessibility={isOpen ? "no-hide-descendants" : "auto"} accessibilityElementsHidden={isOpen}>
					{children}
				</View>
				{edgeClaims > 0 && !isOpen ? (
					<GestureDetector gesture={edgePan}>
						<View style={[styles.edge, { width: EDGE_WIDTH }]} testID="sidebar-edge" />
					</GestureDetector>
				) : null}
				{mounted ? (
					<>
						<Animated.View style={[StyleSheet.absoluteFill, { backgroundColor: t.scrim }, scrimStyle]}>
							<Pressable
								accessibilityRole="button"
								accessibilityLabel="Close sidebar"
								style={StyleSheet.absoluteFill}
								onPress={close}
								testID="sidebar-scrim"
							/>
						</Animated.View>
						<GestureDetector gesture={panelPan}>
							<Animated.View
								style={[styles.panel, { width }, panelStyle]}
								accessibilityViewIsModal
								onAccessibilityEscape={close}
								testID="sidebar-panel"
							>
								{content}
							</Animated.View>
						</GestureDetector>
					</>
				) : null}
			</View>
		</SidebarContext.Provider>
	);
}

const styles = StyleSheet.create({
	fill: { flex: 1 },
	edge: { position: "absolute", top: 0, bottom: 0, left: 0 },
	panel: {
		position: "absolute",
		top: 0,
		bottom: 0,
		left: 0,
		shadowColor: "#000",
		shadowOpacity: 0.25,
		shadowRadius: 16,
		shadowOffset: { width: 4, height: 0 },
		elevation: 16,
	},
});
