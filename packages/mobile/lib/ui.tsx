import { Feather } from "./icons";
import { createContext, memo, useContext, useEffect, type ReactNode } from "react";
import {
	Animated,
	Image,
	Platform,
	Pressable,
	StyleSheet,
	Text,
	View,
	type StyleProp,
	type ViewStyle,
} from "react-native";
import { AppSymbol } from "./AppSymbol";
import { resolveChromeIcon, type ChromeIcon } from "./chromeIcon";
import { haptics } from "./haptics";
import { BREATHE_MS, shouldBreathe } from "./motion";
import { useEnterTransition } from "./motionHooks";
import { usePressFeedback, type PressTone } from "./pressFeedback";
import { Spinner } from "./Spinner";
import { useReducedMotion } from "./useReducedMotion";
import { fontScaleCap, iconSize, radius, space, stateOpacity, touchTarget, type } from "./tokens";
import type { ConnStatus } from "./store";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles } from "./ThemeProvider";
// AO mascot glyph (transparent) shown beside each screen heading.
import MASCOT from "../assets/mascot.png";

/**
 * The one breathing clock every consumer shares.
 *
 * A loop per consumer starts wherever that consumer happened to mount, so the
 * working dots in one list pulse out of step and the list shimmers. One value
 * driven by one loop keeps every dot, lamp and label rising and falling
 * together. The loop runs while anyone holds it and stops with the last holder.
 *
 * Created on first use rather than at import, so loading this module never
 * touches `Animated`.
 */
let breathValue: Animated.Value | undefined;
let breathLoop: Animated.CompositeAnimation | undefined;
let breathHolders = 0;

function sharedBreath(): Animated.Value {
	breathValue ??= new Animated.Value(1);
	return breathValue;
}

/** Keeps the shared loop running until the returned release is called. */
function holdBreath(): () => void {
	breathHolders += 1;
	if (breathHolders === 1) {
		const pulse = sharedBreath();
		breathLoop = Animated.loop(
			Animated.sequence([
				Animated.timing(pulse, {
					toValue: 0.35,
					duration: BREATHE_MS,
					useNativeDriver: true,
				}),
				Animated.timing(pulse, {
					toValue: 1,
					duration: BREATHE_MS,
					useNativeDriver: true,
				}),
			]),
		);
		breathLoop.start();
	}
	return () => {
		breathHolders -= 1;
		if (breathHolders > 0) return;
		breathLoop?.stop();
		breathLoop = undefined;
		// Leave it at full opacity; a stopped loop otherwise freezes mid-fade,
		// which reads as a rendering bug rather than a resting state.
		breathValue?.setValue(1);
	};
}

/**
 * The app's breathing loop, in one place.
 *
 * "Working" is the only state that moves, and it moves the same way wherever it
 * appears: a slow opacity pulse, in step everywhere. Returns the shared animated
 * value to drive it, or `undefined` when the loop must not run — which is every
 * state that is not working, and everything at all under Reduce Motion.
 *
 * The setting is consumed here rather than at the call sites, so honouring it
 * once fixes every consumer — status dots, the connection lamp, project rows —
 * without touching any of them.
 */
export function useBreathing(enabled: boolean): Animated.Value | undefined {
	const reduceMotion = useReducedMotion();
	const animate = shouldBreathe(reduceMotion, enabled);
	useEffect(() => {
		// Deliberately not a zero duration: a zero-length loop is a busy loop, so
		// the animation must not start at all.
		if (!animate) return;
		return holdBreath();
	}, [animate]);
	// Handed out before the effect holds the loop, so a dot mounting into a list
	// that is already breathing joins the current phase on its first frame.
	return animate ? sharedBreath() : undefined;
}

// A gently breathing dot - the only motion in the UI, reserved for "working".
// Every breathing dot follows the shared clock above, so a list of working rows
// pulses as one. Memoized so an unrelated parent re-render skips the dots.
export const Dot = memo(function Dot({
	color,
	size = 9,
	breathing = false,
}: {
	color: string;
	size?: number;
	breathing?: boolean;
}) {
	const pulse = useBreathing(breathing);
	return (
		<Animated.View
			style={{
				width: size,
				height: size,
				borderRadius: size / 2,
				backgroundColor: color,
				opacity: pulse ?? 1,
			}}
		/>
	);
});

// `mascot.png` is the desktop mascot (frontend/assets/ao-mascot.png) cropped to
// its artwork, with the eye sockets filled so they stay dark on light themes.
// Its aspect ratio, as height / width.
export const MASCOT_ASPECT = 200 / 256;
// Where the wand tip sits in the artwork, as fractions of its width and height.
const WAND_TIP_X = 0.972;
const WAND_TIP_Y = 0.032;
const LAMP_DOT = 7;
const LAMP_HALO = 16;

// The mascot, with the tip of its wand doubling as the connection lamp: it glows
// green when the daemon is reachable and goes dark when it isn't.
export function MascotLamp({ status, size = 40 }: { status?: ConnStatus; size?: number }) {
	const t = useTheme();
	const s = useThemedStyles(makeStyles);
	const color = status === "open" ? t.green : status === "connecting" ? t.amber : t.textFaint;
	const lit = status === "open" || status === "connecting";
	const label = status === "open" ? "Connected" : status === "connecting" ? "Connecting" : "Offline";
	// The lamp and halo scale with the logo and are centred on the wand tip, so
	// they stay on it at whatever size the logo is drawn.
	const k = size / 40;
	const height = size * MASCOT_ASPECT;
	const tipX = size * WAND_TIP_X;
	const tipY = height * WAND_TIP_Y;
	const dot = LAMP_DOT * k;
	const halo = LAMP_HALO * k;
	return (
		<View
			style={[s.mascotWrap, { width: size, height }]}
			accessible
			accessibilityRole="image"
			accessibilityLabel={status ? `Orchestrator mascot, ${label}` : "Orchestrator mascot"}
		>
			<Image source={MASCOT} style={{ width: size, height }} resizeMode="contain" />
			{status ? (
				<>
					{/* Halo first, dot on top: RN has no boxShadow, so the glow is a
					    larger translucent circle plus a platform shadow/elevation. */}
					{lit ? <View style={[s.wandHalo, { left: tipX - halo / 2, top: tipY - halo / 2, width: halo, height: halo, borderRadius: halo / 2, backgroundColor: color, shadowColor: color }]} /> : null}
					<View style={[s.wandTip, { left: tipX - dot / 2, top: tipY - dot / 2 }]}>
						<Dot color={color} size={dot} breathing={status === "connecting"} />
					</View>
				</>
			) : null}
		</View>
	);
}

/**
 * A board group's heading. With `onToggle` it folds its section away — same
 * typography and rule as before, plus the chevron that says so.
 *
 * The chevron sits beside the label rather than at the far edge so the row still
 * reads as one heading with a rule running out of it, which is what the board
 * looked like before sections could be folded.
 */
export function ListSectionHeader({
	label,
	count,
	open = true,
	onToggle,
}: {
	label: string;
	count?: number;
	open?: boolean;
	onToggle?: () => void;
}) {
	const t = useTheme();
	const s = useThemedStyles(makeStyles);
	const feedback = usePressFeedback("row");
	const body = (
		<>
			<Text maxFontSizeMultiplier={fontScaleCap.chrome} style={s.listSectionLabel}>
				{label}
			</Text>
			{onToggle ? (
				<AppSymbol name={open ? "chevronDown" : "chevronForward"} size={iconSize.sm} color={t.textTertiary} />
			) : null}
			{count !== undefined ? (
				<Text maxFontSizeMultiplier={fontScaleCap.chrome} style={s.listSectionCount}>
					{count}
				</Text>
			) : null}
		</>
	);

	if (!onToggle) {
		return (
			<View accessible accessibilityRole="header" accessibilityLabel={count === undefined ? label : `${label}, ${count}`} style={s.listSectionHeader}>
				{body}
			</View>
		);
	}
	return (
		<Pressable
			accessibilityRole="button"
			accessibilityState={{ expanded: open }}
			accessibilityLabel={`${label} section`}
			android_ripple={feedback.android_ripple}
			onPress={onToggle}
			style={({ pressed }) => [s.listSectionHeader, pressed && feedback.pressed]}
		>
			{body}
		</Pressable>
	);
}

/**
 * How much a `Button` stands out, each named for the native button it draws
 * like:
 *
 *  - `primary`: the filled accent button, one per screen. iOS
 *    `borderedProminent`, M3 `Button`.
 *  - `secondary`: a tinted fill for an action beside the primary, or for the
 *    one action of an empty or error state. iOS `bordered`, M3
 *    `FilledTonalButton`. On Android it is also what a button in an
 *    `EmptyState` draws as when it names no variant.
 *  - `ghost`: text only, for the least important action in a group. iOS
 *    `plain`, M3 `TextButton`.
 *  - `danger`: a red tint, for an action that destroys something.
 *  - `success`: desktop's green merge action.
 */
export type ButtonVariant = "primary" | "secondary" | "ghost" | "danger" | "success";

/** The variant a `Button` takes when its call site names none. */
const DefaultButtonVariant = createContext<ButtonVariant>("primary");

function buttonColors(t: Theme, variant: ButtonVariant): { fill: string; ink: string; ripple: PressTone } {
	switch (variant) {
		// `onAccent`, not a literal: near-black reads best on the dark theme's light
		// accent, but is invisible on light mode's darker one, which needs white.
		case "primary":
			return { fill: t.accent, ink: t.onAccent, ripple: "onAccent" };
		case "secondary":
			return { fill: t.accentTint, ink: t.accent, ripple: "neutral" };
		case "ghost":
			return { fill: "transparent", ink: t.accent, ripple: "neutral" };
		case "danger":
			return { fill: t.tintRed, ink: t.red, ripple: "destructive" };
		case "success":
			return { fill: t.green, ink: t.bgBase, ripple: "onAccent" };
	}
}

/**
 * The app's text button: a capsule, as both platforms draw theirs. iOS dims it
 * while pressed; Android ripples inside the capsule and never scales.
 *
 * It stays React Native rather than `@expo/ui`'s Button because the call sites
 * lay it out as a flex child (full-width CTAs, button rows, empty-state
 * actions), and a native button sizes itself inside a `Host` that would need
 * measuring per button. It also needs a busy state the native one lacks.
 */
export function Button({
	title,
	onPress,
	variant: namedVariant,
	loading = false,
	disabled = false,
	icon,
	style,
}: {
	title: string;
	onPress: () => void;
	/** `primary` unless the button sits in an `EmptyState`, which picks the platform's own. */
	variant?: ButtonVariant;
	loading?: boolean;
	disabled?: boolean;
	/** A chrome glyph before the title; Lucide names draw as their system symbol. */
	icon?: ChromeIcon;
	style?: StyleProp<ViewStyle>;
}) {
	const t = useTheme();
	const s = useThemedStyles(makeStyles);
	const fallbackVariant = useContext(DefaultButtonVariant);
	const variant = namedVariant ?? fallbackVariant;
	const { fill, ink, ripple } = buttonColors(t, variant);
	const feedback = usePressFeedback("button", { tone: ripple });
	const inactive = disabled || loading;
	return (
		<Pressable
			accessibilityRole="button"
			// The label is pinned to the visible title rather than left to the child
			// text: while a request is in flight the text is replaced by a spinner,
			// and the control would otherwise announce with no name at all.
			accessibilityLabel={title}
			accessibilityState={{ disabled: disabled || loading, busy: loading }}
			android_ripple={feedback.android_ripple}
			onPress={() => {
				// Danger actions get a cautionary buzz; everything else a light tap.
				if (variant === "danger") haptics.warning();
				else haptics.tap();
				onPress();
			}}
			disabled={inactive}
			style={({ pressed }) => [
				s.btn,
				variant === "ghost" && s.btnGhost,
				{ backgroundColor: fill },
				inactive && s.btnInactive,
				pressed && feedback.pressed,
				style,
			]}
		>
			{loading ? (
				<Spinner color={ink} />
			) : (
				<View style={s.btnInner}>
					{icon ? <ChromeGlyph icon={icon} size={iconSize.md} color={ink} /> : null}
					<Text maxFontSizeMultiplier={fontScaleCap.body} style={[s.btnText, { color: ink }]}>
						{title}
					</Text>
				</View>
			)}
		</Pressable>
	);
}

/** A chrome glyph from an icon prop: the system symbol, or Lucide where there is none. */
function ChromeGlyph({ icon, size, color }: { icon: ChromeIcon; size: number; color: string }) {
	const glyph = resolveChromeIcon(icon);
	if (glyph.symbol) return <AppSymbol name={glyph.symbol} size={size} color={color} />;
	return <Feather name={glyph.lucide} size={size} color={color} />;
}

// A numbered instruction row. Used by both onboarding screens: the welcome
// screen passes a `hint` for the full "how it works" list, the scanner omits it
// and passes `compact` so the steps stay one line each above the viewfinder.
// The badge is the same circle either way.
export function NumberedStep({
	n,
	total,
	title,
	hint,
	compact = false,
}: {
	n: number;
	/** How many steps the list has, so a screen reader says "Step 1 of 3". */
	total?: number;
	title: string;
	hint?: string;
	compact?: boolean;
}) {
	const s = useThemedStyles(makeStyles);
	const position = total ? `Step ${n} of ${total}` : `Step ${n}`;
	return (
		// One element for a screen reader, so the number is read with its step
		// rather than as a stray digit before it.
		<View accessible accessibilityLabel={[position, title, hint].filter(Boolean).join(", ")} style={[s.step, compact && s.stepCompact]}>
			<View style={s.stepBadge}>
				<Text maxFontSizeMultiplier={fontScaleCap.chrome} style={s.stepNum}>{n}</Text>
			</View>
			<View style={{ flex: 1 }}>
				<Text style={s.stepTitle}>{title}</Text>
				{hint ? <Text style={s.stepHint}>{hint}</Text> : null}
			</View>
		</View>
	);
}

// The body of a sheet. The sheet chrome itself — the card, the rounded corners,
// the grabber and the drag-to-dismiss — is now the OS's job: these render inside
// native `formSheet` routes (see `app/sheets/*` and the registrations in
// `app/_layout.tsx`). Re-implementing that in JS on top of RN's `Modal` never
// felt native, because `Modal` is a plain container with no gesture support.
/**
 * The title block of a sheet. Separate from `SheetScreen` because the scrolling
 * sheets pass it as their list's `ListHeaderComponent` rather than rendering it
 * as a sibling of the list: a scroller sitting next to static siblings inside a
 * sheet laid the two on top of each other. One scrolling root, header inside it,
 * has no such interplay — and it matches how iOS's own sheets behave.
 */
export function SheetHeader({
	title,
	subtitle,
	right,
}: {
	/**
	 * Left out when a native bar above the sheet already carries the title; the
	 * title row goes with it, `right` included.
	 */
	title?: string;
	subtitle?: string;
	/** Trailing control on the title row — the agent sheet's Refresh lives here. */
	right?: ReactNode;
}) {
	const s = useThemedStyles(makeStyles);
	return (
		<View style={s.sheetHeader}>
			{title ? (
				<View style={s.sheetTitleRow}>
					<Text accessibilityRole="header" style={[s.sheetTitle, { flex: 1 }]}>{title}</Text>
					{right}
				</View>
			) : null}
			{subtitle ? <Text style={s.sheetSubtitle}>{subtitle}</Text> : null}
		</View>
	);
}

/**
 * `contentContainerStyle` for a scrolling sheet — the inset its header and rows
 * share. Pure padding, so it needs no theme and can be a plain const.
 */
export const SHEET_SCROLL_CONTENT = {
	paddingHorizontal: space.xl,
	paddingTop: space.xl,
	paddingBottom: space.xxl,
};

/** A sheet whose content is short and fixed, so it needs no scrolling root. */
export function SheetScreen({
	title,
	subtitle,
	right,
	children,
}: {
	/** Left out when a native bar above the sheet already carries the title. */
	title?: string;
	subtitle?: string;
	right?: ReactNode;
	children: ReactNode;
}) {
	const s = useThemedStyles(makeStyles);
	return (
		<View style={s.sheetScreen}>
			<SheetHeader title={title} subtitle={subtitle} right={right} />
			{children}
		</View>
	);
}

/**
 * A screen or list with nothing to show, laid out as iOS's
 * `ContentUnavailableView` and the M3 pattern both are: a large glyph in the
 * secondary colour standing on its own, a title, a line of explanation, and the
 * action that fixes it.
 *
 * Drawn in React Native on both platforms. SwiftUI's `ContentUnavailableView`
 * needs iOS 17 (the app supports 16.4, where it draws nothing), cannot set the
 * app's typeface, and has no action slot; a native view would also have to be
 * measured inside every flexible container this sits in.
 *
 * It fills a flex parent and centres in it, and otherwise takes the height of
 * its content, so a list gives it room with `contentContainerStyle={{ flexGrow: 1 }}`.
 */
export function EmptyState({
	icon = "inbox",
	title,
	message,
	action,
	pulse = false,
	busy = false,
	visual,
	iconColor,
}: {
	/** A chrome glyph; Lucide names draw as their system symbol. */
	icon?: ChromeIcon;
	title: string;
	message?: string;
	/**
	 * A `Button`, or several: they stack, centred, under the message. One that
	 * names no variant is prominent on iOS and tonal on Android.
	 */
	action?: ReactNode;
	/** Breathe the icon, for a state that is waiting on something. */
	pulse?: boolean;
	/** The system's activity indicator in place of the icon, for a state still loading. */
	busy?: boolean;
	/** Drawn in place of the icon: a mascot, a machine's avatar. */
	visual?: ReactNode;
	/** The glyph's colour. Secondary text unless the state means something, like a failure. */
	iconColor?: string;
}) {
	const t = useTheme();
	const s = useThemedStyles(makeStyles);
	const breathing = useBreathing(pulse);
	// An empty state appears once per screen and then sits still, so it can afford
	// a quiet entrance. Lists and rows stay unstaggered.
	const enter = useEnterTransition(space.xxs);
	return (
		<Animated.View style={[s.empty, enter]}>
			<Animated.View style={[s.emptyVisual, breathing ? { opacity: breathing } : undefined]}>
				{busy ? (
					<Spinner size="large" color={iconColor ?? t.textSecondary} />
				) : (
					visual ?? <ChromeGlyph icon={icon} size={iconSize.hero} color={iconColor ?? t.textSecondary} />
				)}
			</Animated.View>
			<Text accessibilityRole="header" maxFontSizeMultiplier={fontScaleCap.body} style={s.emptyTitle}>
				{title}
			</Text>
			{message ? (
				<Text maxFontSizeMultiplier={fontScaleCap.body} style={s.emptyMsg}>
					{message}
				</Text>
			) : null}
			{action ? (
				// Material leads an empty state with a tonal button where iOS keeps the
				// prominent one. A button that names its variant keeps it, so a pair can
				// still rank one action above the other.
				<DefaultButtonVariant.Provider value={Platform.OS === "android" ? "secondary" : "primary"}>
					<View style={s.emptyActions}>{action}</View>
				</DefaultButtonVariant.Provider>
			) : null}
		</Animated.View>
	);
}

const STEP_BADGE = Math.ceil(type.footnote.lineHeight * fontScaleCap.chrome) + 2 * space.hair;

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		listSectionHeader: {
			flexDirection: "row",
			alignItems: "center",
			gap: space.sm,
			paddingHorizontal: space.lg,
			paddingTop: space.lg,
			paddingBottom: space.xxs,
		},
		listSectionLabel: { fontFamily: "Geist_500Medium", color: t.textTertiary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, fontWeight: "500" },
		// Mono and tabular so a count changing from 9 to 10 does not shift the rule.
		listSectionCount: { color: t.textFaint, fontSize: type.footnote.fontSize, fontWeight: "600", fontFamily: t.fontMono },

		// Enlarged from 30x26: the wand-tip lamp replaced the "live" pill, so the tip
		// has to be big enough for the dot to actually read as a status light.
		mascotWrap: { marginTop: space.hair },
		// Positioned on the wand tip by MascotLamp, which centres them on it.
		// zIndex, not paint order: Android orders siblings by elevation/zIndex, and
		// the halo used to carry `elevation` — which put a 28%-opacity circle *over*
		// the dot and washed it out. Stating both keeps the dot on top everywhere.
		wandTip: { position: "absolute", zIndex: 1 },
		wandHalo: {
			position: "absolute",
			opacity: 0.28,
			zIndex: 0,
			// iOS-only; Android has no glow-style shadow, so there the halo circle
			// alone stands in for it. No `elevation` — that draws a drop shadow
			// beneath the view and reorders it, neither of which is a glow.
			shadowOpacity: 0.9,
			shadowRadius: 6,
			shadowOffset: { width: 0, height: 0 },
		},

		// The platform's touch target is the floor, so a one-line button is exactly
		// that tall and a wrapped title grows it. `overflow` keeps the Android
		// ripple inside the capsule.
		btn: {
			minHeight: touchTarget,
			borderRadius: radius.pill,
			overflow: "hidden",
			paddingVertical: space.sm,
			paddingHorizontal: space.xl,
			alignItems: "center",
			justifyContent: "center",
		},
		// Text only: just enough inset for the press state to frame the title.
		btnGhost: { paddingHorizontal: space.md },
		btnInactive: { opacity: stateOpacity.disabled },
		btnInner: { flexDirection: "row", alignItems: "center", gap: space.xs },
		btnText: { fontFamily: "Geist_600SemiBold", fontSize: type.subheadline.fontSize, lineHeight: type.subheadline.lineHeight, fontWeight: "600", textAlign: "center" },

		step: {
			flexDirection: "row",
			alignItems: "flex-start",
			gap: space.md,
			paddingVertical: space.md,
		},
		stepCompact: { paddingVertical: space.xs, alignItems: "center", gap: space.md },
		// No fixed box for the number to overflow. The floor is the number's line
		// at the largest scale it allows plus the padding, so one digit always
		// sits in a circle, and two stretch it into a capsule.
		stepBadge: {
			minWidth: STEP_BADGE,
			minHeight: STEP_BADGE,
			paddingHorizontal: space.xs,
			paddingVertical: space.hair,
			borderRadius: radius.pill,
			backgroundColor: t.accentTint,
			alignItems: "center",
			justifyContent: "center",
		},
		stepNum: { fontFamily: "Geist_600SemiBold", color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, fontWeight: "600", fontVariant: ["tabular-nums"] },
		stepTitle: { fontFamily: "Geist_600SemiBold", color: t.textPrimary, fontSize: type.subheadline.fontSize, fontWeight: "600" },
		stepHint: { fontFamily: "Geist_400Regular",
			color: t.textTertiary,
			fontSize: type.footnote.fontSize,
			lineHeight: type.footnote.lineHeight,
			marginTop: space.hair,
		},

		// No card, corners or grabber here — the native sheet draws all of that. The
		// top padding leaves room for the OS grabber so the title doesn't sit under it.
		// No bottom safe-area inset on either of these: the native sheet already
		// reserves room for the home indicator, and adding it again left a dead strip.
		sheetScreen: {
			backgroundColor: t.bgSurface,
			paddingHorizontal: space.xl,
			paddingTop: space.xl,
			paddingBottom: space.xl,
		},
		// Padding lives on the scroll content rather than a wrapper, so a scrolling
		// sheet's list can run edge to edge while its rows keep the same inset.
		sheetHeader: { paddingBottom: space.hair },
		sheetTitleRow: { flexDirection: "row", alignItems: "center", gap: space.md },
		sheetTitle: { fontFamily: "Geist_600SemiBold",
			color: t.textPrimary,
			fontSize: type.title3.fontSize,
			fontWeight: "600",
			letterSpacing: -0.3,
		},
		sheetSubtitle: { fontFamily: "Geist_400Regular",
			color: t.textSecondary,
			fontSize: type.footnote.fontSize,
			lineHeight: type.footnote.lineHeight,
			marginTop: space.xxs,
		},

		empty: {
			flex: 1,
			alignItems: "center",
			justifyContent: "center",
			paddingHorizontal: space.xxxl,
			paddingVertical: space.huge,
		},
		emptyVisual: { marginBottom: space.lg },
		emptyTitle: {
			...type.title3,
			color: t.textPrimary,
			textAlign: "center",
		},
		emptyMsg: {
			...type.subheadline,
			color: t.textSecondary,
			textAlign: "center",
			marginTop: space.sm,
			maxWidth: 300,
		},
		emptyActions: { marginTop: space.xl, alignItems: "center", gap: space.sm },
	});
