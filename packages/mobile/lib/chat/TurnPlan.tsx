/**
 * The agent's plan for a turn, as a live checklist — the desktop panel
 * (frontend/src/renderer/components/chat/TurnPlan.tsx) drawn for a phone.
 *
 * One panel per turn, not a row per revision: the provider re-sends the whole plan
 * every time it changes and the daemon overwrites the turn's copy, so this is
 * current state. Ticking a step changes no layout: the row keeps its height and only
 * the mark and the tone change, so a plan settling under a reader's eyes never moves
 * the text they are on.
 *
 * A long plan scrolls inside a fixed window that follows the step being worked on,
 * fading at whichever edge has more to show. Once every step is done the list folds
 * to its header — the count already says how it went — and opens again if the agent
 * re-plans. Steps read at body-adjacent size: the status is carried by the mark and
 * the tone, not by a label beside every row.
 */

import { useCallback, useEffect, useRef, useState } from "react";
import { type LayoutChangeEvent, type NativeScrollEvent, type NativeSyntheticEvent, Pressable, ScrollView, StyleSheet, Text, View } from "react-native";
import Animated, { useAnimatedStyle, useSharedValue, withTiming } from "react-native-reanimated";
import Svg, { Circle, Defs, LinearGradient, Path, Rect, Stop } from "react-native-svg";

import { haptics } from "../haptics";
import { Feather } from "../icons";
import { LAYOUT_MS } from "../motion";
import type { Theme } from "../theme";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { font, press, radius, space, type } from "../tokens";
import { Spinning } from "../ui";
import { useReducedMotion } from "../useReducedMotion";
import {
	activePlanStep,
	PLAN_EDGE_FADE,
	PLAN_STEP_LABEL,
	PLAN_WINDOW_HEIGHT,
	planAccessibilityLabel,
	planEdgeFades,
	planFollowOffset,
} from "./planModel";
import { FoldBody } from "./TurnFoldMotion";
import type { ConversationPlan, PlanStep } from "./types";

export function TurnPlan({
	plan,
	live,
}: {
	plan: ConversationPlan;
	/** The turn is still running, so the plan can still change. */
	live?: boolean;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const reduceMotion = useReducedMotion();
	const steps = plan.steps;
	const done = steps.filter((step) => step.status === "completed").length;
	const allDone = steps.length > 0 && done === steps.length;
	const [expanded, setExpanded] = useState(() => !allDone);
	const wasAllDone = useRef(allDone);
	const activeStep = activePlanStep(steps);

	// Fold when the last step is ticked, open again if the agent re-plans. Only on
	// the change, so a reader who reopens a finished plan keeps it open.
	useEffect(() => {
		if (wasAllDone.current === allDone) return;
		wasAllDone.current = allDone;
		setExpanded(!allDone);
	}, [allDone]);

	const scrollRef = useRef<ScrollView>(null);
	const rows = useRef(new Map<number, { y: number; height: number }>());
	const viewportHeight = useRef(0);
	const contentHeight = useRef(0);
	const offset = useRef(0);
	const followed = useRef(false);
	const [fades, setFades] = useState({ top: false, bottom: false });

	const syncFades = useCallback(() => {
		const next = planEdgeFades(offset.current, viewportHeight.current, contentHeight.current);
		setFades((current) => (current.top === next.top && current.bottom === next.bottom ? current : next));
	}, []);

	// Keep the step being worked on in view, centred so the finished step before it
	// stays visible as context. Scrolls only this window, never the transcript, and
	// lands instantly the first time so a plan scrolled back into view does not glide.
	const follow = useCallback(() => {
		const row = rows.current.get(activeStep);
		if (!row || !viewportHeight.current || !contentHeight.current) return;
		const top = planFollowOffset(row, viewportHeight.current, contentHeight.current);
		scrollRef.current?.scrollTo({ y: top, animated: followed.current && !reduceMotion });
		offset.current = top;
		followed.current = true;
		syncFades();
	}, [activeStep, reduceMotion, syncFades]);

	useEffect(() => {
		if (!expanded) {
			followed.current = false;
			rows.current.clear();
			return;
		}
		follow();
	}, [expanded, follow, steps.length]);

	if (steps.length === 0) return null;
	// Folded, the header still says where the agent is.
	const currentStep = !expanded && !allDone ? steps[activeStep]?.text : undefined;

	return (
		<View style={styles.card} accessibilityState={{ busy: Boolean(live) && !allDone }}>
			{/* The whole header is the toggle, so the target is the row rather than an icon. */}
			<Pressable
				accessibilityRole="button"
				accessibilityState={{ expanded }}
				accessibilityLabel={planAccessibilityLabel(steps)}
				accessibilityHint={expanded ? "Collapses the plan" : "Expands the plan"}
				onPress={() => {
					haptics.tap();
					setExpanded((open) => !open);
				}}
				style={({ pressed }) => [styles.header, pressed && { opacity: press.opacity }]}
			>
				<PlanMark done={allDone} />
				<Text numberOfLines={1} style={styles.title}>Plan</Text>
				<Text numberOfLines={1} style={styles.current}>{currentStep}</Text>
				{/* Tabular so the count does not jitter the header as it climbs. */}
				<Text style={styles.count}>{done}/{steps.length}</Text>
				<HeaderChevron open={expanded} color={t.textFaint} />
			</Pressable>

			{expanded ? (
				<FoldBody>
					{plan.explanation ? <Text style={styles.explanation}>{plan.explanation}</Text> : null}
					<View style={styles.window}>
						<ScrollView
							ref={scrollRef}
							nestedScrollEnabled
							scrollEventThrottle={16}
							showsVerticalScrollIndicator={false}
							style={{ maxHeight: PLAN_WINDOW_HEIGHT }}
							contentContainerStyle={styles.list}
							onLayout={(event: LayoutChangeEvent) => {
								viewportHeight.current = event.nativeEvent.layout.height;
								follow();
							}}
							onContentSizeChange={(_, height) => {
								contentHeight.current = height;
								follow();
							}}
							onScroll={(event: NativeSyntheticEvent<NativeScrollEvent>) => {
								offset.current = event.nativeEvent.contentOffset.y;
								syncFades();
							}}
						>
							{steps.map((step, index) => (
								<View
									key={`${index}-${step.text}`}
									accessible
									accessibilityLabel={`${step.text}, ${PLAN_STEP_LABEL[step.status]}`}
									onLayout={(event: LayoutChangeEvent) => {
										const { y, height } = event.nativeEvent.layout;
										rows.current.set(index, { y, height });
										if (index === activeStep) follow();
									}}
									style={styles.step}
								>
									<StepMark status={step.status} live={Boolean(live)} />
									<Text
										style={[
											styles.stepText,
											step.status === "completed" && styles.stepDone,
											step.status === "in_progress" && styles.stepActive,
										]}
									>
										{step.text}
									</Text>
								</View>
							))}
						</ScrollView>
						{fades.top ? <EdgeFade edge="top" color={t.bgSurface} /> : null}
						{fades.bottom ? <EdgeFade edge="bottom" color={t.bgSurface} /> : null}
					</View>
				</FoldBody>
			) : null}
		</View>
	);
}

/** A chevron that points down while folded and turns up as the plan opens. */
function HeaderChevron({ open, color }: { open: boolean; color: string }) {
	const reduceMotion = useReducedMotion();
	const progress = useSharedValue(open ? 1 : 0);
	useEffect(() => {
		progress.value = withTiming(open ? 1 : 0, { duration: reduceMotion ? 0 : LAYOUT_MS });
	}, [open, progress, reduceMotion]);
	const style = useAnimatedStyle(() => ({ transform: [{ rotate: `${progress.value * 180}deg` }] }));
	return (
		<Animated.View style={style}>
			<Feather name="chevron-down" size={16} color={color} />
		</Animated.View>
	);
}

/** The header's mark: the list icon while there is work left, a filled tick once it is all done. */
function PlanMark({ done }: { done: boolean }) {
	const t = useTheme();
	if (!done) return <Feather name="list-checks" size={17} color={t.textTertiary} />;
	// A finished plan is completed work, not an actionable success, so the tick takes
	// the success hue at reduced strength rather than full green.
	return (
		<Svg width={18} height={18} viewBox="0 0 18 18">
			<Circle cx={9} cy={9} r={9} fill={t.green} fillOpacity={0.7} />
			<Path d="M5.6 9.3 7.9 11.6 12.5 6.8" fill="none" stroke={t.bgSurface} strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" />
		</Svg>
	);
}

/**
 * The mark for one step, in a fixed-size slot the height of one text line, so a
 * spinner that turns into a tick never nudges the lines below it.
 */
function StepMark({ status, live }: { status: PlanStep["status"]; live: boolean }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	return (
		<View style={styles.markSlot}>
			{status === "pending" ? (
				// Not started: a dashed ring, present but quiet.
				<Svg width={16} height={16} viewBox="0 0 16 16">
					<Circle cx={8} cy={8} r={6.75} fill="none" stroke={t.textFaint} strokeWidth={1.25} strokeDasharray="2.6 2.7" />
				</Svg>
			) : status === "in_progress" ? (
				// In progress: a faint ring with an arc turning round it, neutral like the
				// turn's own Working indicator. It turns only while the turn runs: a turn
				// that is stopped or fails, and a plan the agent keeps across turns, leave a
				// step in progress, and a spinner there would say the agent is still at it.
				// Held still under Reduce Motion too.
				<Spinning enabled={live}>
					<Svg width={16} height={16} viewBox="0 0 16 16">
						<Circle cx={8} cy={8} r={6.75} fill="none" stroke={t.textPrimary} strokeOpacity={0.18} strokeWidth={1.5} />
						<Circle cx={8} cy={8} r={6.75} fill="none" stroke={t.textPrimary} strokeWidth={1.5} strokeLinecap="round" strokeDasharray="12 43" />
					</Svg>
				</Spinning>
			) : (
				// Done: an outlined circle with a tick, in the finished rows' quiet tone.
				<Svg width={16} height={16} viewBox="0 0 16 16">
					<Circle cx={8} cy={8} r={6.75} fill="none" stroke={t.textTertiary} strokeWidth={1.25} />
					<Path d="M5.3 8.2 7.2 10.1 10.8 6.3" fill="none" stroke={t.textTertiary} strokeWidth={1.5} strokeLinecap="round" strokeLinejoin="round" />
				</Svg>
			)}
		</View>
	);
}

/** The surface colour fading out over an edge that has more steps behind it. */
function EdgeFade({ edge, color }: { edge: "top" | "bottom"; color: string }) {
	const id = `plan-fade-${edge}`;
	// Both gradients run top to bottom and the bottom edge swaps the opacities:
	// react-native-svg on Android drew nothing for a reversed y1="1" y2="0" gradient.
	const start = edge === "top" ? 1 : 0;
	return (
		<View pointerEvents="none" style={[fadeStyles.fade, edge === "top" ? { top: 0 } : { bottom: 0 }]}>
			<Svg width="100%" height="100%">
				<Defs>
					<LinearGradient id={id} x1="0" y1="0" x2="0" y2="1">
						<Stop offset="0" stopColor={color} stopOpacity={start} />
						<Stop offset="1" stopColor={color} stopOpacity={1 - start} />
					</LinearGradient>
				</Defs>
				<Rect width="100%" height="100%" fill={`url(#${id})`} />
			</Svg>
		</View>
	);
}

const fadeStyles = StyleSheet.create({
	fade: { position: "absolute", left: 0, right: 0, height: PLAN_EDGE_FADE },
});

const makeStyles = (t: Theme) => StyleSheet.create({
	card: {
		backgroundColor: t.bgSurface,
		borderRadius: radius.md,
		borderCurve: "continuous",
		borderWidth: StyleSheet.hairlineWidth,
		borderColor: t.borderDefault,
		overflow: "hidden",
	},
	header: { minHeight: 48, flexDirection: "row", alignItems: "center", gap: space.sm + 2, paddingHorizontal: space.md + 2 },
	title: { ...type.subheadline, fontFamily: font.medium, fontWeight: "500", color: t.textPrimary, flexShrink: 0 },
	current: { ...type.subheadline, flex: 1, color: t.textTertiary },
	count: { ...type.footnote, fontFamily: font.medium, fontWeight: "500", color: t.textTertiary, fontVariant: ["tabular-nums"] },
	explanation: { ...type.footnote, color: t.textTertiary, paddingHorizontal: space.md + 2, paddingBottom: space.xs },
	window: { position: "relative" },
	list: { paddingHorizontal: space.md + 2, paddingBottom: space.md },
	step: { flexDirection: "row", alignItems: "flex-start", gap: space.sm + 2, paddingVertical: 5 },
	markSlot: { width: 16, height: type.subheadline.lineHeight, alignItems: "center", justifyContent: "center" },
	stepText: { ...type.subheadline, flex: 1, color: t.textSecondary },
	stepActive: { color: t.textPrimary },
	stepDone: { color: t.textTertiary, textDecorationLine: "line-through" },
});
