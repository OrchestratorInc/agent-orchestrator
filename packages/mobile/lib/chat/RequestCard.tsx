import { useCallback, useState } from "react";
import { Pressable, StyleSheet, Text, TextInput, View, type AccessibilityRole } from "react-native";
import { Directions, Gesture, GestureDetector } from "react-native-gesture-handler";
import Animated, { SlideInLeft, SlideInRight, SlideOutLeft, SlideOutRight } from "react-native-reanimated";

import { AppSymbol } from "../AppSymbol";
import { rowDividerWidth } from "../divider";
import { haptics } from "../haptics";
import { PAGE_SLIDE_MS } from "../motion";
import { usePressFeedback } from "../pressFeedback";
import { Spinner } from "../Spinner";
import type { Theme } from "../theme";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { textSelectionColors } from "../textSelection";
import { fontScaleCap, iconSize, radius, space, stateOpacity, touchTarget, type } from "../tokens";
import { useReducedMotion } from "../useReducedMotion";
import { toggleInputValue } from "./elicitationModel";
import type { RequestDockModel, RequestDockPage } from "./requestDockModel";
import { userFacingError } from "../connectionError";

/** The arrow that points back up the timeline, to the card that owns the request. */
const towardCard = { ios: "arrow.up", android: "arrow_upward" } as const;

/**
 * The pending request, standing in for the composer.
 *
 * A blocking question is the next thing you have to do, so it takes the place
 * you were going to type in rather than sitting above it as a pointer to a card
 * somewhere up the timeline. Multiple questions page in place — chevrons, or a
 * swipe.
 *
 * The inline cards stay in the timeline as the record, and anything this card
 * cannot express honestly (a form, a URL handshake) is handed back to them.
 */
export function RequestCard({
	model,
	onDecide,
	onResolveInput,
	onShow,
	onDismiss,
}: {
	model: RequestDockModel;
	onDecide(requestId: string, decisionId: string): Promise<void>;
	onResolveInput(requestId: string, action: "accept" | "decline" | "cancel", content?: Record<string, unknown>): Promise<void>;
	onShow(sequence: number): void;
	onDismiss(): void;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const reduceMotion = useReducedMotion();
	const iconFeedback = usePressFeedback("icon");
	const rowFeedback = usePressFeedback("row");
	const textFeedback = usePressFeedback("button");
	const sendFeedback = usePressFeedback("button", { tone: "onAccent" });
	const [page, setPage] = useState(0);
	const [values, setValues] = useState<Record<string, unknown>>({});
	const [draft, setDraft] = useState("");
	const [submitting, setSubmitting] = useState(false);
	const [error, setError] = useState<string>();
	// Which way the next page should travel: forward pages come in from the
	// right, back pages from the left, so the motion agrees with the swipe.
	const [forward, setForward] = useState(true);

	const total = model.pages.length;
	const current: RequestDockPage | undefined = model.pages[page];
	const busy = model.busy || submitting;

	const goTo = useCallback((next: number) => {
		if (next < 0 || next >= total) return;
		setForward(next > page);
		haptics.select();
		setError(undefined);
		// Restore what was typed for the page being shown. Clearing it meant
		// stepping away and back silently discarded an answer already given.
		const target = model.pages[next];
		const stored = target?.freeText ? values[target.freeText.name] : undefined;
		setDraft(stored === undefined || stored === null ? "" : String(stored));
		setPage(next);
	}, [model.pages, page, total, values]);

	// A fling is enough for "swipe between questions" and never competes with the
	// vertical scroll behind the card.
	const swipe = Gesture.Race(
		Gesture.Fling().direction(Directions.RIGHT).onEnd(() => goTo(page - 1)).runOnJS(true),
		Gesture.Fling().direction(Directions.LEFT).onEnd(() => goTo(page + 1)).runOnJS(true),
	);

	if (!current) return null;

	const skippable = !current.required && model.kind === "input" && Boolean(model.requestId);

	// One haptic per answer, when it lands: the press itself only starts the work.
	const run = (work: Promise<void>) => {
		setSubmitting(true);
		setError(undefined);
		work
			.then(() => haptics.success())
			.catch((cause) => {
				setError(userFacingError(cause));
				haptics.error();
			})
			.finally(() => setSubmitting(false));
	};

	// Past the last question, send the lot; otherwise on to the next one.
	const advance = (next: Record<string, unknown>) => {
		if (page >= total - 1) run(onResolveInput(model.requestId ?? "", "accept", next));
		else goTo(page + 1);
	};

	const answer = (value: unknown, property?: string) => {
		if (busy || !model.requestId) return;
		if (model.kind === "approval") {
			run(onDecide(model.requestId, String(value)));
			return;
		}
		const name = property ?? current.propertyName;
		if (!name) return;
		const next = { ...values, [name]: value };
		setValues(next);
		advance(next);
	};

	// Several answers: each option toggles, and the question moves on only when
	// asked to.
	const toggle = (value: string) => {
		const name = current.propertyName;
		if (busy || !name) return;
		const chosen = values[name];
		setValues({ ...values, [name]: toggleInputValue(Array.isArray(chosen) ? chosen : [], value) });
		haptics.select();
	};
	const chosen = current.propertyName ? values[current.propertyName] : undefined;
	const multiAnswered = Array.isArray(chosen) && chosen.length > 0;
	const canContinue = Boolean(model.requestId) && !busy && (multiAnswered || !current.required);

	const sendTyped = () => {
		const typed = draft.trim();
		if (!typed || !current.freeText) return;
		answer(current.freeText.numeric ? Number(typed) : typed, current.freeText.name);
	};

	const dismiss = (
		<Pressable
			accessibilityRole="button"
			accessibilityLabel="Dismiss and type instead"
			android_ripple={iconFeedback.android_ripple}
			onPress={onDismiss}
			style={({ pressed }) => [styles.iconButton, pressed && iconFeedback.pressed]}
		>
			<AppSymbol name="close" size={iconSize.md} color={t.textTertiary} />
		</Pressable>
	);

	// Deferred requests get a name and a way back to the card that owns them.
	if (!model.canAnswerInline) {
		return (
			<View style={styles.card}>
				<View style={styles.head}>
					<Text accessibilityRole="header" maxFontSizeMultiplier={fontScaleCap.chrome} style={styles.eyebrow}>{model.title}</Text>
					{dismiss}
				</View>
				{current.question ? <Text maxFontSizeMultiplier={fontScaleCap.title} style={styles.question}>{current.question}</Text> : null}
				<Pressable
					accessibilityRole="button"
					accessibilityLabel="Open the full request"
					android_ripple={textFeedback.android_ripple}
					onPress={() => onShow(model.sequence)}
					style={({ pressed }) => [styles.openForm, pressed && textFeedback.pressed]}
				>
					<AppSymbol name={towardCard} size={iconSize.sm} color={t.accent} />
					<Text maxFontSizeMultiplier={fontScaleCap.chrome} style={styles.openFormText}>Open the full request</Text>
				</Pressable>
			</View>
		);
	}

	// An approval's options are its decisions, each an action. A question's are
	// its answers: one of several, or any number of them.
	const optionRole: AccessibilityRole = model.kind === "approval" ? "button" : current.multi ? "checkbox" : "radio";
	const atStart = page === 0;
	const atEnd = page >= total - 1;

	return (
		<GestureDetector gesture={swipe}>
			<View style={styles.card}>
				<View style={styles.head}>
					{total > 1 ? <View style={styles.pager}>
						<Pressable
							accessibilityRole="button"
							accessibilityLabel="Previous question"
							accessibilityState={{ disabled: atStart }}
							disabled={atStart}
							android_ripple={iconFeedback.android_ripple}
							onPress={() => goTo(page - 1)}
							style={({ pressed }) => [styles.iconButton, pressed && iconFeedback.pressed]}
						>
							<AppSymbol name="chevronBackward" size={iconSize.md} color={atStart ? t.textFaint : t.textSecondary} />
						</Pressable>
						<Text maxFontSizeMultiplier={fontScaleCap.chrome} style={styles.pagerText}>{page + 1} of {total}</Text>
						<Pressable
							accessibilityRole="button"
							accessibilityLabel="Next question"
							accessibilityState={{ disabled: atEnd }}
							disabled={atEnd}
							android_ripple={iconFeedback.android_ripple}
							onPress={() => goTo(page + 1)}
							style={({ pressed }) => [styles.iconButton, pressed && iconFeedback.pressed]}
						>
							<AppSymbol name="chevronForward" size={iconSize.md} color={atEnd ? t.textFaint : t.textSecondary} />
						</Pressable>
					</View> : <Text accessibilityRole="header" maxFontSizeMultiplier={fontScaleCap.chrome} style={styles.eyebrow}>{model.title}</Text>}
					{/* One exit, in one place. Skip takes the corner where it exists;
					    otherwise — an approval, or a required question — the close holds
					    it, since the card has replaced the composer and that is the only
					    way back to it. */}
					{skippable ? (
						<Pressable
							accessibilityRole="button"
							accessibilityLabel="Skip this question"
							accessibilityState={{ disabled: busy }}
							disabled={busy}
							android_ripple={textFeedback.android_ripple}
							onPress={() => { if (page < total - 1) goTo(page + 1); else run(onResolveInput(model.requestId ?? "", "accept", values)); }}
							style={({ pressed }) => [styles.skip, pressed && textFeedback.pressed, busy && styles.dim]}
						>
							<Text maxFontSizeMultiplier={fontScaleCap.chrome} style={styles.skipText}>Skip</Text>
						</Pressable>
					) : dismiss}
				</View>

				<Animated.View
					key={page}
					entering={reduceMotion ? undefined : (forward ? SlideInRight : SlideInLeft).duration(PAGE_SLIDE_MS)}
					exiting={reduceMotion ? undefined : (forward ? SlideOutLeft : SlideOutRight).duration(PAGE_SLIDE_MS)}
				>
					{current.question ? <Text maxFontSizeMultiplier={fontScaleCap.title} style={styles.question}>{current.question}</Text> : null}

					<View accessibilityRole={optionRole === "radio" ? "radiogroup" : undefined}>
						{current.options.map((option, index) => {
							// The answers were always kept — they accumulate until the last
							// page submits them — but nothing said so on a second visit.
							const selected = current.multi
								? Array.isArray(chosen) && chosen.includes(option.id)
								: current.propertyName ? values[current.propertyName] === option.id : false;
							return (
								<Pressable
									key={option.id}
									accessibilityRole={optionRole}
									accessibilityLabel={option.label}
									accessibilityHint={option.description}
									accessibilityState={optionRole === "button" ? { disabled: busy } : { disabled: busy, checked: selected }}
									disabled={busy}
									android_ripple={rowFeedback.android_ripple}
									onPress={() => { if (current.multi) toggle(option.id); else answer(option.id); }}
									style={({ pressed }) => [styles.option, index > 0 && styles.optionDivider, pressed && rowFeedback.pressed, busy && styles.dim]}
								>
									<View style={[styles.ordinal, selected && styles.ordinalSelected]}>
										{selected
											? <AppSymbol name="checkmark" size={iconSize.xs} color={t.bgBase} />
											: <Text maxFontSizeMultiplier={fontScaleCap.chrome} style={styles.ordinalText}>{index + 1}</Text>}
									</View>
									<View style={styles.optionBody}>
										<Text maxFontSizeMultiplier={fontScaleCap.body} style={[styles.optionLabel, selected && styles.optionLabelSelected]}>{option.label}</Text>
										{option.description ? <Text maxFontSizeMultiplier={fontScaleCap.body} style={styles.optionHint}>{option.description}</Text> : null}
									</View>
								</Pressable>
							);
						})}
					</View>

					{current.multi && current.options.length ? <View style={styles.continueRow}>
						<Pressable
							accessibilityRole="button"
							accessibilityLabel={atEnd ? "Send answers" : "Next question"}
							accessibilityState={{ disabled: !canContinue, busy: busy && !draft.trim() }}
							disabled={!canContinue}
							android_ripple={sendFeedback.android_ripple}
							onPress={() => advance(values)}
							style={({ pressed }) => [styles.send, pressed && sendFeedback.pressed, !canContinue && styles.dim]}
						>
							{busy && !draft.trim() ? <Spinner color={t.onAccent} /> : <AppSymbol name={atEnd ? "send" : "chevronForward"} size={iconSize.sm} color={t.onAccent} />}
						</Pressable>
					</View> : null}

					{current.freeText ? <View style={[styles.typed, current.options.length > 0 && styles.optionDivider]}>
						<View style={styles.ordinal}><AppSymbol name="rename" size={iconSize.xs} color={t.textTertiary} /></View>
						<TextInput
							accessibilityLabel={current.freeText.placeholder}
							editable={!busy}
							value={draft}
							onChangeText={setDraft}
							placeholder={current.freeText.placeholder}
							placeholderTextColor={t.textTertiary}
							{...textSelectionColors(t)}
							keyboardType={current.freeText.numeric ? "number-pad" : "default"}
							maxLength={current.freeText.maxLength}
							returnKeyType="send"
							onSubmitEditing={sendTyped}
							maxFontSizeMultiplier={fontScaleCap.body}
							style={styles.typedInput}
						/>
						{draft.trim() ? <Pressable
							accessibilityRole="button"
							accessibilityLabel="Send answer"
							accessibilityState={{ disabled: busy, busy }}
							disabled={busy}
							android_ripple={sendFeedback.android_ripple}
							onPress={sendTyped}
							style={({ pressed }) => [styles.send, pressed && sendFeedback.pressed]}
						>
							{busy ? <Spinner color={t.onAccent} /> : <AppSymbol name="send" size={iconSize.sm} color={t.onAccent} />}
						</Pressable> : null}
					</View> : null}
				</Animated.View>


				{busy && !draft.trim() && !current.multi ? <View style={styles.busy}><Spinner color={t.textTertiary} /></View> : null}
				{error ? <Text accessibilityRole="alert" maxFontSizeMultiplier={fontScaleCap.chrome} style={styles.error}>{error}</Text> : null}
			</View>
		</GestureDetector>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		card: {
			backgroundColor: t.bgElevated,
			borderWidth: rowDividerWidth,
			borderColor: t.borderDefault,
			borderRadius: radius.xl,
			borderCurve: "continuous",
			paddingHorizontal: space.md,
			paddingTop: space.xxs,
			paddingBottom: space.xs,
			overflow: "hidden",
		},
		// The chevrons, Skip and the close are full touch targets; the row is as
		// tall as they are and pulls them out to the card's edge.
		head: { flexDirection: "row", alignItems: "center", justifyContent: "space-between", minHeight: touchTarget, marginRight: -space.sm },
		pager: { flexDirection: "row", alignItems: "center", marginLeft: -space.sm },
		iconButton: { width: touchTarget, height: touchTarget, alignItems: "center", justifyContent: "center" },
		pagerText: { fontFamily: "DMSans_600SemiBold", color: t.textSecondary, fontSize: type.footnote.fontSize, fontWeight: "600", fontVariant: ["tabular-nums"] },
		eyebrow: { fontFamily: "DMSans_600SemiBold", color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, fontWeight: "600" },
		question: { fontFamily: "DMSans_600SemiBold", color: t.textPrimary, fontSize: type.body.fontSize, lineHeight: type.body.lineHeight, fontWeight: "600", marginTop: space.xxs, marginBottom: space.sm },
		option: { minHeight: touchTarget, flexDirection: "row", alignItems: "center", gap: space.md, paddingVertical: space.md },
		optionDivider: { borderTopWidth: rowDividerWidth, borderTopColor: t.borderSubtle },
		ordinal: { width: 26, height: 26, borderRadius: radius.pill, alignItems: "center", justifyContent: "center", backgroundColor: t.bgSubtle },
		ordinalText: { fontFamily: "DMSans_600SemiBold", color: t.textSecondary, fontSize: type.footnote.fontSize, fontWeight: "600" },
		// Neutral, like the Orchestrator badge: the filled circle and the check
		// carry the selection, so it needs no colour of its own.
		ordinalSelected: { backgroundColor: t.textSecondary },
		optionLabelSelected: { fontFamily: "DMSans_600SemiBold", fontWeight: "600" },
		optionBody: { flex: 1, minWidth: 0, gap: space.hair },
		optionLabel: { fontFamily: "DMSans_400Regular", color: t.textPrimary, fontSize: type.subheadline.fontSize, lineHeight: type.subheadline.lineHeight },
		optionHint: { fontFamily: "DMSans_400Regular", color: t.textTertiary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
		continueRow: { flexDirection: "row", justifyContent: "flex-end", paddingVertical: space.xs },
		typed: { flexDirection: "row", alignItems: "center", gap: space.md, paddingVertical: space.xs },
		typedInput: { fontFamily: "DMSans_400Regular", flex: 1, minHeight: touchTarget, color: t.textPrimary, fontSize: type.subheadline.fontSize, paddingVertical: space.sm },
		send: { width: touchTarget, height: touchTarget, borderRadius: radius.pill, overflow: "hidden", alignItems: "center", justifyContent: "center", backgroundColor: t.accent },
		skip: { minWidth: touchTarget, minHeight: touchTarget, alignItems: "center", justifyContent: "center", paddingHorizontal: space.sm, borderRadius: radius.pill, overflow: "hidden" },
		skipText: { fontFamily: "DMSans_600SemiBold", color: t.textSecondary, fontSize: type.footnote.fontSize, fontWeight: "600" },
		openForm: { minHeight: touchTarget, flexDirection: "row", alignItems: "center", gap: space.xs },
		openFormText: { fontFamily: "DMSans_600SemiBold", color: t.accent, fontSize: type.subheadline.fontSize, fontWeight: "600" },
		busy: { paddingVertical: space.sm, alignItems: "center" },
		error: { fontFamily: "DMSans_400Regular", color: t.red, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, paddingBottom: space.xs },
		dim: { opacity: stateOpacity.disabled },
	});
