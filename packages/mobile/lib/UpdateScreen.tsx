import { useEffect, useSyncExternalStore } from "react";
import { Modal, StyleSheet, Text, useWindowDimensions, View } from "react-native";
import Animated, {
	cancelAnimation,
	Easing,
	useAnimatedStyle,
	useSharedValue,
	withDelay,
	withRepeat,
	withTiming,
} from "react-native-reanimated";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { useTheme, useThemeState } from "./ThemeProvider";
import { space, type } from "./tokens";
import { Button } from "./ui";
import { UpdateMascot } from "./UpdateMascot";
import { FINALE_MS } from "./updateMascotPose";
import {
	applyUpdate,
	dismissUpdateScreen,
	getUpdateScreen,
	subscribeUpdateScreen,
	type UpdateScreenPhase,
} from "./updateScreenState";
import { reloadIntoUpdate } from "./updateResumeStore";
import { useReducedMotion } from "./useReducedMotion";

const BAR_W = 168;
const SEGMENT_W = 48;

/**
 * Full-screen "update ready" moment: once an over-the-air update has downloaded,
 * Ruto stands over a Restart button, breathing, blinking, a sheen passing over
 * it (tap Ruto and it squishes). The app restarts only on that tap, after Ruto
 * nods; Not now (or Android back) returns to the app. Mounted once in the root
 * layout; raised through updateScreenState.ts.
 *
 * A Modal rather than an absolute overlay: Settings is a native form sheet, and
 * a view in the root hierarchy would sit underneath it.
 */
export function UpdateScreen() {
	const screen = useSyncExternalStore(subscribeUpdateScreen, getUpdateScreen);
	return (
		<Modal
			visible={screen.visible}
			transparent
			animationType="fade"
			statusBarTranslucent
			navigationBarTranslucent
			onRequestClose={dismissUpdateScreen}
		>
			{screen.visible ? <UpdateScreenBody phase={screen.phase} /> : null}
		</Modal>
	);
}

function UpdateScreenBody({ phase }: { phase: UpdateScreenPhase }) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const { width } = useWindowDimensions();
	const insets = useSafeAreaInsets();
	const restarting = phase === "restarting";
	// The words fade out with Ruto (the last 40% of the finale), so nothing is
	// left on screen alone before the reload.
	const copy = useSharedValue(1);
	useEffect(() => {
		if (restarting) copy.value = withDelay(FINALE_MS * 0.6, withTiming(0, { duration: FINALE_MS * 0.4 }));
	}, [restarting, copy]);
	const copyStyle = useAnimatedStyle(() => ({ opacity: copy.value }));

	return (
		<View
			style={[styles.root, { backgroundColor: t.bgBase, paddingBottom: insets.bottom + space.lg }]}
			accessibilityViewIsModal
		>
			<View style={styles.stage}>
				<UpdateMascot
					size={Math.min(280, width * 0.68)}
					shadowColor={scheme === "dark" ? "rgba(255,255,255,0.08)" : "rgba(10,11,13,0.10)"}
					finale={restarting}
				/>
				<Animated.View style={[styles.copy, copyStyle]}>
					<Text style={[styles.title, { color: t.textPrimary }]} accessibilityRole="header">
						Update ready
					</Text>
					{/* Both blocks below stay laid out in either phase and only swap
					    visibility, so nothing moves when the buttons give way to the
					    progress line. */}
					<View
						style={!restarting && styles.hidden}
						accessibilityElementsHidden={!restarting}
						importantForAccessibility={restarting ? "auto" : "no-hide-descendants"}
					>
						<View style={[styles.track, { backgroundColor: t.bgSubtle }]}>
							{restarting ? <Segment color={t.textSecondary} /> : null}
						</View>
						<Text style={[styles.status, { color: t.textSecondary }]} accessibilityLiveRegion="polite">
							{restarting ? "Restarting…" : " "}
						</Text>
					</View>
				</Animated.View>
			</View>
			<View
				style={[styles.actions, restarting && styles.hidden]}
				pointerEvents={restarting ? "none" : "auto"}
				accessibilityElementsHidden={restarting}
				importantForAccessibility={restarting ? "no-hide-descendants" : "auto"}
			>
				<Button
					title="Restart to update"
					onPress={() => {
						applyUpdate({ reloadAsync: reloadIntoUpdate }).catch((e) => console.warn("[updates] reload failed", e));
					}}
				/>
				<Button title="Not now" variant="ghost" onPress={dismissUpdateScreen} />
			</View>
		</View>
	);
}

function Segment({ color }: { color: string }) {
	const reduceMotion = useReducedMotion();
	const x = useSharedValue(0);
	useEffect(() => {
		if (reduceMotion) return;
		x.value = withRepeat(withTiming(1, { duration: 1100, easing: Easing.inOut(Easing.quad) }), -1, true);
		return () => cancelAnimation(x);
	}, [reduceMotion, x]);
	const style = useAnimatedStyle(() => ({ transform: [{ translateX: x.value * (BAR_W - SEGMENT_W) }] }));
	return <Animated.View style={[styles.fill, { width: SEGMENT_W, backgroundColor: color }, style]} />;
}

const styles = StyleSheet.create({
	root: { flex: 1, paddingHorizontal: space.xxl },
	stage: { flex: 1, alignItems: "center", justifyContent: "center" },
	copy: { alignItems: "center" },
	title: { ...type.title2, marginTop: space.xxl, textAlign: "center" },
	track: { width: BAR_W, height: 3, borderRadius: 1.5, overflow: "hidden", marginTop: space.lg },
	fill: { height: 3, borderRadius: 1.5 },
	status: { ...type.footnote, marginTop: space.sm, textAlign: "center" },
	actions: { alignSelf: "stretch", gap: space.sm },
	hidden: { opacity: 0 },
});
