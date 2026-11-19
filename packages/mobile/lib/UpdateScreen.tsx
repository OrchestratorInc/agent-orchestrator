import { useEffect, useSyncExternalStore } from "react";
import { Modal, StyleSheet, Text, useWindowDimensions, View } from "react-native";
import Animated, {
	cancelAnimation,
	Easing,
	useAnimatedStyle,
	useSharedValue,
	withRepeat,
	withTiming,
} from "react-native-reanimated";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { useTheme, useThemeState } from "./ThemeProvider";
import { space, type } from "./tokens";
import { Button } from "./ui";
import { UpdateMascot } from "./UpdateMascot";
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
 * the mascot hops and waves its wand over a Restart button. The app restarts
 * only on that tap; Not now (or Android back) returns to the app. Mounted once
 * in the root layout; raised through updateScreenState.ts.
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

	return (
		<View
			style={[styles.root, { backgroundColor: t.bgBase, paddingBottom: insets.bottom + space.lg }]}
			accessibilityViewIsModal
		>
			<View style={styles.stage}>
				<UpdateMascot
					size={Math.min(260, width * 0.64)}
					shadowColor={scheme === "dark" ? "rgba(255,255,255,0.08)" : "rgba(10,11,13,0.10)"}
				/>
				<Text style={[styles.title, { color: t.textPrimary }]} accessibilityRole="header">
					Update ready
				</Text>
				{/* Both blocks below stay laid out in either phase and only swap
				    visibility, so nothing moves when the buttons give way to the
				    progress line. */}
				<View style={!restarting && styles.hidden} importantForAccessibility={restarting ? "auto" : "no-hide-descendants"}>
					<View style={[styles.track, { backgroundColor: t.bgSubtle }]}>
						{restarting ? <Segment color={t.textSecondary} /> : null}
					</View>
					<Text style={[styles.status, { color: t.textSecondary }]} accessibilityLiveRegion="polite">
						{restarting ? "Restarting…" : " "}
					</Text>
				</View>
			</View>
			<View
				style={[styles.actions, restarting && styles.hidden]}
				pointerEvents={restarting ? "none" : "auto"}
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
	title: { ...type.title2, marginTop: space.xxl, textAlign: "center" },
	track: { width: BAR_W, height: 3, borderRadius: 1.5, overflow: "hidden", marginTop: space.lg },
	fill: { height: 3, borderRadius: 1.5 },
	status: { ...type.footnote, marginTop: space.sm, textAlign: "center" },
	actions: { alignSelf: "stretch", gap: space.sm },
	hidden: { opacity: 0 },
});
