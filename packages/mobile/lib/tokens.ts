/**
 * The app's measurement system. Every size, gap, radius, icon and duration in
 * the product comes from here, so a screen never invents a number.
 *
 * The type follows T3 Code's mobile app, whose look this app shares:
 *
 *   - **DM Sans** for text and Geist Mono for code. Native chrome the system
 *     draws itself (navigation titles, tab labels) stays in SF Pro / Roboto.
 *   - **A compact ramp**: 16/23 reading text, 14/19 secondary copy and a 13pt
 *     floor for metadata. Each step keeps the name of the Apple text style it
 *     scales with, which is what `fontScaleCap` is calibrated against.
 *   - **Weights of 400, 500 and 600 only.** Row titles and labels are medium;
 *     semibold is for titles and the rare emphasised word.
 *   - **No tracking at reading sizes.** DM Sans is spaced for text already; only
 *     the largest titles tighten a little.
 *
 * Spacing is a 4pt grid with a 2pt half-step for dense rows. Radii, icon sizes
 * and motion follow the same idea: a short ladder of named steps chosen for a
 * job, not a number picked per screen.
 */

export type TypeToken = {
	fontFamily: string;
	fontSize: number;
	lineHeight: number;
	fontWeight: "400" | "500" | "600";
	letterSpacing: number;
};

/**
 * The app's family, one file per weight.
 *
 * A weight is a font file here, not a number: with a bundled family iOS draws
 * whichever face the name points at and ignores `fontWeight`, so a style that
 * sets a weight has to name the file that goes with it. `fontForWeight` is that
 * mapping in one place, and the type ramp below uses it.
 */
export const font = {
	regular: "DMSans_400Regular",
	medium: "DMSans_500Medium",
	semibold: "DMSans_600SemiBold",
	mono: "GeistMono_400Regular",
} as const;

export function fontForWeight(weight: TypeToken["fontWeight"]): string {
	return weight === "600" ? font.semibold : weight === "500" ? font.medium : font.regular;
}

/** The tracking scale, in em. */
export const tracking = { tightXl: -0.02, tightLg: -0.01, tight: 0 } as const;

/**
 * The ramp. Use the semantic name, not the number: a screen title is
 * `title2`, a row label is `body`, a metadata line is `footnote`.
 *
 *   largeTitle / title1 / title2  — screen and sheet titles
 *   title3                        — section titles inside a screen
 *   headline                      — emphasized body (row titles, primary copy)
 *   body                          — default reading size
 *   callout                       — slightly denser body
 *   subheadline                   — secondary copy under a row title
 *   footnote                      — metadata, timestamps, counts
 *   caption1                      — dense labels, tab bar
 *   caption2                      — the floor: badges and micro-labels
 */
export const type = {
	largeTitle: { fontFamily: font.semibold, fontSize: 30, lineHeight: 36, fontWeight: "600", letterSpacing: 30 * tracking.tightXl },
	title1: { fontFamily: font.semibold, fontSize: 26, lineHeight: 32, fontWeight: "600", letterSpacing: 26 * tracking.tightXl },
	title2: { fontFamily: font.semibold, fontSize: 21, lineHeight: 28, fontWeight: "600", letterSpacing: 21 * tracking.tightLg },
	title3: { fontFamily: font.semibold, fontSize: 18, lineHeight: 23, fontWeight: "600", letterSpacing: 18 * tracking.tightLg },
	headline: { fontFamily: font.medium, fontSize: 16, lineHeight: 23, fontWeight: "500", letterSpacing: 16 * tracking.tight },
	body: { fontFamily: font.regular, fontSize: 16, lineHeight: 23, fontWeight: "400", letterSpacing: 16 * tracking.tight },
	callout: { fontFamily: font.regular, fontSize: 15, lineHeight: 21, fontWeight: "400", letterSpacing: 15 * tracking.tight },
	subheadline: { fontFamily: font.regular, fontSize: 14, lineHeight: 19, fontWeight: "400", letterSpacing: 14 * tracking.tight },
	footnote: { fontFamily: font.regular, fontSize: 13, lineHeight: 17, fontWeight: "400", letterSpacing: 13 * tracking.tight },
	caption1: { fontFamily: font.medium, fontSize: 12, lineHeight: 16, fontWeight: "500", letterSpacing: 12 * tracking.tight },
	caption2: { fontFamily: font.medium, fontSize: 11, lineHeight: 14, fontWeight: "500", letterSpacing: 0 },
} as const satisfies Record<string, TypeToken>;

/**
 * Long-form copy inside a message. The HIG line heights above are tuned for
 * labels and rows; prose wants the looser 1.5 leading that keeps a paragraph
 * readable, so it gets its own step rather than borrowing the callout's.
 */
export const prose: TypeToken = {
	fontFamily: font.regular,
	fontSize: 16,
	lineHeight: 24,
	fontWeight: "400",
	letterSpacing: 16 * tracking.tight,
};

export type TypeName = keyof typeof type;

/** 4pt grid, with a 2pt half-step for hairline gaps inside a row. */
export const space = {
	none: 0,
	hair: 2,
	xxs: 4,
	xs: 6,
	sm: 8,
	md: 12,
	lg: 16,
	xl: 20,
	xxl: 24,
	xxxl: 32,
	huge: 40,
} as const;

/** Horizontal padding for screen content and list rows. */
export const gutter = space.lg;

/**
 * Radii. Nesting reads as one surface only when the inner radius is the outer
 * radius minus the padding between them — `concentric()` does that arithmetic
 * so a card and its inset children never disagree by a pixel.
 */
export const radius = {
	xs: 4,
	sm: 8,
	md: 12,
	lg: 16,
	xl: 20,
	xxl: 28,
	pill: 999,
} as const;

/**
 * Colours drawn over a live camera preview. The preview is dark whatever the
 * theme, so these stay fixed: the backdrop behind it before the first frame
 * arrives, and the reticle that shows where to hold a code.
 */
export const onCamera = {
	backdrop: "#0c0d10",
	reticle: "rgba(255,255,255,0.92)",
} as const;

/** Icon ladder. Match the stroke to the weight of the text beside it. */
export const iconSize = {
	xs: 12,
	sm: 15,
	md: 17,
	lg: 20,
	xl: 24,
	xxl: 28,
	/** The large glyph an empty state leads with. */
	hero: 48,
} as const;

/**
 * The minimum tappable square: 44pt on iOS (HIG), 48dp on Android (Material 3).
 * Size a control's hit area with it, never its picture: a glyph or avatar that
 * borrows it grows on Android with the target.
 */
export { touchTarget } from "./platformTokens";

/**
 * A form sheet's `sheetCornerRadius`: the system's own shape on iOS (unset) and
 * Material 3's bottom-sheet corner on Android. Pass this rather than a number,
 * so a sheet keeps the platform's shape.
 */
export { sheetCornerRadius } from "./platformTokens";

/**
 * The inset every sheet body shares. The native sheet draws its own top
 * clearance, so this is the one place the JS side pads — keeping it here is what
 * stops two pickers from disagreeing about their top edge.
 */
export const sheetInset = {
	horizontal: space.xl,
	top: space.xl,
	bottom: space.xxl,
} as const;

/**
 * Motion. Fast and short for anything the user triggers repeatedly; longer
 * only when something enters the screen for the first time.
 *
 * The standard curve is a decelerating ease — quick off the mark, settled at
 * the end — which is what makes a transition feel like the system's own.
 */
export const duration = {
	instant: 0,
	fast: 100,
	base: 150,
	slow: 220,
	slower: 320,
} as const;

export const easingCurve = {
	/** cubic-bezier control points — quick off the mark, settled at the end. */
	standard: [0.2, 0, 0, 1] as const,
	decelerate: [0.2, 0, 0, 1] as const,
	accelerate: [0.4, 0, 1, 1] as const,
} as const;

/** Spring configs for RN's Animated. */
export const spring = {
	/** Settles without overshoot — sheets, sidebars, anything structural. */
	gentle: { damping: 24, stiffness: 220, mass: 0.9 },
	/** A little faster, still no bounce — press feedback, toggles. */
	snappy: { damping: 20, stiffness: 320, mass: 0.8 },
} as const;

/**
 * Opacity for a control's interactive states, one value each so every control
 * dims by the same amount. `pressed` is for iOS controls that fade on touch; an
 * Android control shows a ripple instead (see `pressFeedback.ts`). `disabled`
 * sits at Material 3's 38% for disabled content, rounded.
 */
export const stateOpacity = {
	pressed: 0.7,
	disabled: 0.4,
} as const;

/** Press feedback: scale down, fade a touch, and come back on the same curve. */
export const press = {
	scale: 0.96,
	opacity: stateOpacity.pressed,
	in: duration.fast,
	out: duration.base,
} as const;

/**
 * How far each kind of text may grow under Dynamic Type.
 *
 * Uncapped text breaks this app specifically: the worker dock is a fixed 52pt,
 * rows are fixed-height controls, and chips and eyebrows are laid out expecting
 * one line. At the largest accessibility sizes those clip or draw under a
 * neighbouring control rather than reflowing.
 *
 * This is the accessibility exception, not a policy: content keeps the user's
 * full setting, and a cap tight enough to defeat it is worse than a layout that
 * bends — hence the 1.3 floor.
 */
export const fontScaleCap = {
	/** Chips, badges, pills, eyebrows, row values — dense and single-line. */
	chrome: 1.3,
	/** Screen and sheet titles. */
	title: 1.4,
	/** Body copy, buttons, settings labels, empty states. */
	body: 1.6,
} as const;
