// Mission Control palette. Color = meaning; most states get none.
//
//   neutral (accent) = interactive emphasis: primary actions, selected controls
//   orange           = a working agent (alive, running)
//   amber            = needs your input / attention
//   red              = failing / stuck / crashed
//   green            = mergeable / passed / approved
//   purple           = merged
//   neutral (muted)  = terminal states — done, killed
//
// Purple was added for merged, which was the one state that needed its own hue:
// it borrowed the muted grey shared with "done" and "killed", so a pull request
// that landed looked like a session that had merely switched off, sitting next
// to a green that had only just said "mergeable". Purple means exactly that one
// thing and nothing else.
//
// Blue is the stopped/exited worker treatment, matching the desktop board.
// Interactive emphasis stays hue-less so a filled button cannot be mistaken
// for a worker state; only links, selection and the cursor carry a hue.
//
// The terminal's ANSI ramp further down is the one exception: a terminal's 16
// colors are a compatibility surface owned by the agent TUIs, not part of this
// palette, and agents print in them by name.
//
// Two palettes of one shape, following T3 Code's mobile palette: neutral zinc
// surfaces, hue-less prominent actions, a blue reserved for links and focus.
// Semantic colours are DARKENED for light mode, not reused. A hue tuned for
// contrast against #0a0a0a is washed out on white, and skipping this is what
// makes naive light themes look faded.
//
// There is deliberately no exported `theme` const. A default would let a screen
// keep rendering dark while compiling cleanly; without one, `tsc` names every
// call site that still needs the hook.

import { DEFAULT_THEME_STYLE, getThemeDefinition, type ThemeStyle } from "../../shared/theme/palettes";
import { deriveMobileTheme, deriveTerminalTheme } from "./themeDerive";

export type Theme = {
	bgBase: string;
	bgColumn: string;
	bgSurface: string;
	bgElevated: string;
	bgElevatedHover: string;
	bgSubtle: string;

	textPrimary: string;
	textSecondary: string;
	textTertiary: string;
	textFaint: string;

	borderSubtle: string;
	borderDefault: string;
	borderStrong: string;

	/** Interactive emphasis. Deliberately hue-less — see the note at the top. */
	accent: string;
	accentTint: string;
	accentBorder: string;
	accentPressed: string;
	/**
	 * The theme's own accent, for links, text selection and the cursor. The
	 * default palette keeps these hue-less, so it equals `accent` there; a named
	 * theme puts its signature colour here and nowhere a state could be implied.
	 */
	highlight: string;
	/**
	 * Android's text-selection fill: the highlight, translucent so the selected
	 * text stays legible (iOS draws its own tint from `highlight`). The default
	 * palette keeps it equal to `accentBorder`.
	 */
	selection: string;

	orange: string;
	blue: string;
	amber: string;
	red: string;
	green: string;
	purple: string;

	tintOrange: string;
	tintBlue: string;
	tintAmber: string;
	tintRed: string;
	tintGreen: string;
	tintPurple: string;

	/** Ink on a filled accent surface (primary button, send, mic). */
	onAccent: string;
	/** Ink on a filled `red` surface (the recording mic): white, as the system draws it. */
	onDestructive: string;
	/**
	 * Ink, text included, on a filled `green` or `purple` surface (the success
	 * button, the merge track): white where it reads, dark on a dark theme's pale
	 * fills.
	 */
	onStatus: string;
	/** Scrim behind modals. */
	scrim: string;

	fontMono: string;
};

/** The renderer's mono: `--font-family-mono` starts at Geist Mono. */
const FONT_MONO = "GeistMono_400Regular";

export const darkTheme: Theme = {
	// Neutral zinc, after T3 Code: a near-black canvas with no blue cast, cards
	// a single step up, and borders kept to a whisper. Flat rows on the canvas do
	// the grouping; a card is the only bordered surface.
	bgBase: "#0a0a0a",
	bgColumn: "#0a0a0a",
	// Sheets and flush chrome: iOS's own elevated tone, so a sheet stands off
	// the dimmed page under it instead of matching it.
	bgSurface: "#1c1c1e",
	// Cards & inputs, a clear step up from both the canvas (1.33:1) and a sheet,
	// so a grouped card reads as one; T3's own step was too faint on a phone.
	bgElevated: "#27272a",
	bgElevatedHover: "#303034",
	bgSubtle: "rgba(255,255,255,0.04)",

	// Metadata reads at 6.4:1 on the canvas and 4.7:1 on a card; the
	// decorative step at 3.7:1.
	textPrimary: "#f5f5f5",
	textSecondary: "#a1a1a1",
	textTertiary: "#939393",
	textFaint: "#6b6b6b",

	borderSubtle: "rgba(255,255,255,0.07)",
	borderDefault: "rgba(255,255,255,0.11)",
	borderStrong: "rgba(255,255,255,0.17)",

	accent: "#f5f5f5",
	accentTint: "rgba(245,245,245,0.12)",
	accentBorder: "rgba(245,245,245,0.26)",
	accentPressed: "rgba(245,245,245,0.2)",
	// Links, selection and the cursor take T3's blue. A filled control never
	// does, so the hue still cannot be read as a worker state.
	highlight: "#4b7cf3",
	selection: "rgba(52,107,241,0.32)",

	orange: "#ff8904",
	blue: "#60a5fa",
	amber: "#ffb900",
	red: "#f8585b",
	green: "#00d492",
	purple: "#a684ff",

	tintOrange: "rgba(255,137,4,0.14)",
	tintBlue: "rgba(96,165,250,0.14)",
	tintAmber: "rgba(255,185,0,0.14)",
	tintRed: "rgba(248,88,91,0.14)",
	tintGreen: "rgba(0,212,146,0.14)",
	tintPurple: "rgba(166,132,255,0.14)",

	// Near-black ink on the near-white accent.
	onAccent: "#0a0a0a",
	onDestructive: "#ffffff",
	// The page colour: white is under 2.4:1 on these pale fills.
	onStatus: "#0a0a0a",
	scrim: "rgba(0,0,0,0.56)",

	fontMono: FONT_MONO,
};

export const lightTheme: Theme = {
	// T3 Code's light canvas: near-white, with grouped cards a clear zinc step
	// darker (1.16:1) rather than white cards on grey. The step is what
	// separates a card on a phone; a border alone was too faint to.
	bgBase: "#fcfcfc",
	bgColumn: "#fcfcfc",
	bgSurface: "#ffffff",
	bgElevated: "#ebebee",
	bgElevatedHover: "#e2e2e6",
	bgSubtle: "rgba(0,0,0,0.035)",

	// Secondary 7.5:1, tertiary 5.4:1, faint (decorative) 3.3:1 on the canvas;
	// tertiary still clears 4.5:1 on a card.
	textPrimary: "#27272a",
	textSecondary: "#52525c",
	textTertiary: "#66666f",
	textFaint: "#8c8c96",

	// Alpha rather than solid hex, mirroring the dark ramp's structure so a
	// hairline behaves the same over a card and over the canvas.
	borderSubtle: "rgba(0,0,0,0.07)",
	borderDefault: "rgba(0,0,0,0.11)",
	borderStrong: "rgba(0,0,0,0.18)",

	accent: "#27272a",
	accentTint: "rgba(39,39,42,0.08)",
	accentBorder: "rgba(39,39,42,0.24)",
	accentPressed: "rgba(39,39,42,0.16)",
	highlight: "#1b4ed8",
	selection: "rgba(27,78,216,0.24)",

	// Hue kept, darkened for contrast on white — each clears 4.5:1 both on the
	// canvas and on its own tint, which is where these actually render.
	orange: "#b23a0a",
	blue: "#2563eb",
	amber: "#875900",
	red: "#c10007",
	green: "#007a55",
	purple: "#7f22fe",

	tintOrange: "rgba(178,58,10,0.1)",
	tintBlue: "rgba(37,99,235,0.1)",
	tintAmber: "rgba(135,89,0,0.1)",
	tintRed: "rgba(193,0,7,0.08)",
	tintGreen: "rgba(0,122,85,0.1)",
	tintPurple: "rgba(127,34,254,0.1)",

	onAccent: "#ffffff",
	onDestructive: "#ffffff",
	onStatus: "#ffffff",
	scrim: "rgba(0,0,0,0.32)",

	fontMono: FONT_MONO,
};

export type ColorScheme = "light" | "dark";

// Derived palettes are cached so each (style, scheme) pair keeps one identity:
// useThemedStyles memoises on the theme object, and a fresh object per render
// would rebuild every stylesheet.
const derivedThemes = new Map<string, Theme>();

/**
 * The palette to render. `style` is a named theme from the shared catalog
 * (packages/shared/theme/palettes.ts); the default, Orchestrate, is the
 * hand-tuned palette above rather than a derivation.
 */
export function themeFor(scheme: ColorScheme, style: ThemeStyle = DEFAULT_THEME_STYLE): Theme {
	if (style === DEFAULT_THEME_STYLE) return scheme === "light" ? lightTheme : darkTheme;
	const key = `${style}:${scheme}`;
	let theme = derivedThemes.get(key);
	if (!theme) {
		theme = deriveMobileTheme(getThemeDefinition(style)[scheme], scheme, FONT_MONO);
		derivedThemes.set(key, theme);
	}
	return theme;
}

// ---- Terminal --------------------------------------------------------------

// The terminal's own ramp, kept apart from the product palette: agent TUIs own
// the semantic meaning of the ANSI slots, so a red from a test runner must not
// be redefined by our "failing" red. Values ported from the desktop's
// frontend/src/renderer/lib/terminal-themes.ts + tokens.css.
export type TerminalTheme = {
	background: string;
	foreground: string;
	cursor: string;
	black: string;
	red: string;
	green: string;
	yellow: string;
	blue: string;
	magenta: string;
	cyan: string;
	white: string;
	brightBlack: string;
	brightRed: string;
	brightGreen: string;
	brightYellow: string;
	brightBlue: string;
	brightMagenta: string;
	brightCyan: string;
	brightWhite: string;
};

const darkTerminal: TerminalTheme = {
	background: "#0a0a0a",
	foreground: "#f5f5f5",
	cursor: "#f59f4c",
	// Collapsed into the background on purpose, so a TUI that fills a row with
	// "black" draws an invisible band instead of a bar. Agent TUIs use this slot
	// only as a fill — as a foreground it would be unreadable on their own dark
	// canvas — so nothing legitimate is lost, and the user's submitted prompt sits
	// on the terminal background in both themes rather than in a black stripe.
	black: "#0a0a0a",
	red: "#f05d5e",
	green: "#44c97a",
	yellow: "#e5c34b",
	blue: "#5b9cff",
	magenta: "#c678dd",
	cyan: "#56b6c2",
	white: "#d7dae0",
	brightBlack: "#7f8792",
	brightRed: "#ff7b7c",
	brightGreen: "#62df91",
	brightYellow: "#f2d66d",
	brightBlue: "#79b1ff",
	brightMagenta: "#d99aee",
	brightCyan: "#79d4df",
	brightWhite: "#f5f5f5",
};

const lightTerminal: TerminalTheme = {
	background: "#fafafa",
	foreground: "#24292f",
	cursor: "#b45309",
	// See the dark palette: same reasoning, and the reason the prompt band was a
	// stark black bar on a light canvas.
	black: "#fafafa",
	red: "#a13c37",
	green: "#2e6b3e",
	yellow: "#87660f",
	blue: "#3b5aa6",
	magenta: "#7b5799",
	cyan: "#3d7a7a",
	white: "#666d75",
	brightBlack: "#4c535b",
	brightRed: "#7e3330",
	brightGreen: "#265231",
	brightYellow: "#6b5108",
	brightBlue: "#31487f",
	brightMagenta: "#5f4476",
	brightCyan: "#316061",
	brightWhite: "#24292f",
};

export function terminalTheme(scheme: ColorScheme, style: ThemeStyle = DEFAULT_THEME_STYLE): TerminalTheme {
	if (style === DEFAULT_THEME_STYLE) return scheme === "light" ? lightTerminal : darkTerminal;
	return deriveTerminalTheme(getThemeDefinition(style)[scheme], scheme);
}

// ---- Status vocabulary -----------------------------------------------------

// AO's attention levels, in urgency order. Drives the board sections.
export type AttentionLevel = "merge" | "action" | "respond" | "review" | "pending" | "working" | "done";

export type AttentionMeta = { label: string; color: string; tint: string; order: number };

export function attentionMetaFor(t: Theme): Record<string, AttentionMeta> {
	return {
		merge: { label: "Ready to merge", color: t.green, tint: t.tintGreen, order: 0 },
		action: { label: "Needs you", color: t.amber, tint: t.tintAmber, order: 1 },
		respond: { label: "Needs you", color: t.amber, tint: t.tintAmber, order: 1 },
		review: { label: "Review", color: t.red, tint: t.tintRed, order: 2 },
		pending: { label: "In review", color: t.textTertiary, tint: t.bgSubtle, order: 3 },
		working: { label: "Working", color: t.orange, tint: t.tintOrange, order: 4 },
		done: { label: "Done", color: t.textTertiary, tint: t.bgSubtle, order: 5 },
	};
}

export type StatusVisual = { color: string; label: string; breathing?: boolean };

// One status maps to one dot color and short label. Mirrors AO's getStatusSpec so
// the phone speaks the same visual language as the dashboard.
export function statusVisual(t: Theme, status?: string | null): StatusVisual {
	switch (status) {
		case "spawning":
			return { color: t.textSecondary, label: "Starting" };
		case "working":
			return { color: t.orange, label: "Working", breathing: true };
		case "detecting":
			return { color: t.orange, label: "Detecting", breathing: true };
		case "needs_input":
			return { color: t.amber, label: "Needs input" };
		case "changes_requested":
			return { color: t.amber, label: "Changes req." };
		// Unresolved comments from a non-blocking review (not a formal
		// changes-requested decision) -- see agent-orchestrator#5765.
		case "commented":
			return { color: t.amber, label: "Commented" };
		case "stuck":
			return { color: t.red, label: "Stuck" };
		case "errored":
			return { color: t.red, label: "Crashed" };
		case "ci_failed":
			return { color: t.red, label: "CI failed" };
		case "pr_open":
			return { color: t.textSecondary, label: "PR open" };
		case "review_pending":
			return { color: t.textSecondary, label: "In review" };
		case "approved":
			return { color: t.green, label: "Approved" };
		case "mergeable":
			return { color: t.green, label: "Mergeable" };
		// Merged used to drop out of the success hue into the same muted grey as
		// "done" and "killed", which made a landed pull request look like a session
		// that had merely switched off. It has its own hue now: one state, one colour.
		case "merged":
			return { color: t.purple, label: "Merged" };
		case "done":
			return { color: t.green, label: "Done" };
		case "idle":
			return { color: t.textTertiary, label: "Idle" };
		// Without these four the wire value leaks into the UI verbatim — the board
		// was rendering a literal "no_signal". Labels from desktop's
		// sessionStatusViews so the two apps say the same words.
		case "no_signal":
			return { color: t.textTertiary, label: "No signal" };
		case "exited":
			return { color: t.blue, label: "Exited" };
		case "draft":
			return { color: t.textSecondary, label: "Draft PR" };
		case "unknown":
			return { color: t.textTertiary, label: "Unknown" };
		case "cleanup":
			return { color: t.textTertiary, label: "Cleanup" };
		case "killed":
		case "terminated":
			return { color: t.textFaint, label: "Terminated" };
		default:
			return { color: t.textTertiary, label: status ?? "unknown" };
	}
}
