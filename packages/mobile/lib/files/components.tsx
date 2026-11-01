import { memo, useEffect, useMemo, useState } from "react";
import {
	Pressable,
	StyleSheet,
	Text,
	TextInput,
	View,
	useWindowDimensions,
	type AccessibilityActionEvent,
} from "react-native";
import { KeyboardStickyView, useKeyboardState } from "react-native-keyboard-controller";
import { SvgXml } from "react-native-svg";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { ComposerGlass, composerGlassSupported } from "../chat/composer-glass";
import { COMPOSER_FIELD_HEIGHT, COMPOSER_FIELD_MAX_HEIGHT, COMPOSER_HEIGHT, COMPOSER_RADIUS } from "../chat/composerStyles";
import { dockRestingInset, KEYBOARD_DOCK_GAP } from "../session/keyboardInset";
import { highlightCode, type SyntaxTokenKind } from "../chat/syntaxHighlight";
import { AppSymbol } from "../AppSymbol";
import { rowDividerWidth } from "../divider";
import { haptics } from "../haptics";
import { usePressFeedback } from "../pressFeedback";
import { Spinner } from "../Spinner";
import type { Theme } from "../theme";
import { textSelectionColors } from "../textSelection";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { font, fontScaleCap, iconSize, radius, space, stateOpacity, touchTarget, type } from "../tokens";
import type { WorkspaceFileStatus } from "./api";
import { CODE_FONT_SIZE, CODE_LINE_HEIGHT, CODE_MAX_SCALE, codeScale, gutterWidth, MARKER_WIDTH } from "./codeMetrics";
import { fileIconSvg, folderIconSvg } from "./fileTypeIcon";
import { plural, type LineRow } from "./model";
import type { ReviewComment } from "./reviewStore";

// ---- Status + counts ------------------------------------------------------------

function statusColor(status: WorkspaceFileStatus, t: Theme): string {
	switch (status) {
		case "added": return t.green;
		case "deleted": return t.red;
		case "renamed": return t.blue;
		case "modified": return t.amber;
		default: return t.textTertiary;
	}
}

/** Desktop's status letters (lib/workspace-file-status.ts), so a status reads the same in both apps. */
const STATUS_LETTER: Record<WorkspaceFileStatus, string> = { added: "A", deleted: "D", modified: "M", renamed: "R", unmodified: "" };

/** The square every file and folder glyph sits in, so names line up down a list. */
export const FILE_ICON_SLOT = 28;
const FILE_GLYPH = 22;

/**
 * A file as editors show it: the logo of its type (the same Symbols icons
 * desktop draws), with desktop's status letter — A, M, D, R in its colour — at
 * the bottom right. Decorative: every row that uses it already names the file
 * and its status.
 */
export const FileTypeIcon = memo(function FileTypeIcon({ path, status }: { path: string; status?: WorkspaceFileStatus }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const xml = useMemo(() => fileIconSvg(path), [path]);
	const badge = status && status !== "unmodified" ? status : undefined;
	return (
		<View accessible={false} importantForAccessibility="no-hide-descendants" style={styles.typeIcon}>
			<SvgXml xml={xml} width={FILE_GLYPH} height={FILE_GLYPH} opacity={status === "deleted" ? 0.55 : 1} />
			{badge ? <Text maxFontSizeMultiplier={1} style={[styles.statusLetter, { color: statusColor(badge, t) }]}>{STATUS_LETTER[badge]}</Text> : null}
		</View>
	);
});

export const FolderTypeIcon = memo(function FolderTypeIcon({ name }: { name: string }) {
	const styles = useThemedStyles(makeStyles);
	const xml = useMemo(() => folderIconSvg(name), [name]);
	return (
		<View accessible={false} importantForAccessibility="no-hide-descendants" style={styles.typeIcon}>
			<SvgXml xml={xml} width={FILE_GLYPH} height={FILE_GLYPH} />
		</View>
	);
});

export function LineCounts({ additions, deletions, binary }: { additions: number; deletions: number; binary?: boolean }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	if (binary) return <Text maxFontSizeMultiplier={fontScaleCap.chrome} style={[styles.counts, { color: t.textTertiary }]}>binary</Text>;
	return (
		<Text maxFontSizeMultiplier={fontScaleCap.chrome} style={styles.counts}>
			{additions ? <Text style={{ color: t.green }}>+{additions}</Text> : null}
			{additions && deletions ? " " : null}
			{deletions ? <Text style={{ color: t.red }}>−{deletions}</Text> : null}
			{!additions && !deletions ? <Text style={{ color: t.textTertiary }}>±0</Text> : null}
		</Text>
	);
}

// ---- Diff lines ---------------------------------------------------------------------

/** The selection's bar and the knobs at its ends, which share its centre line. */
const SELECTION_BAR_INSET = 3;
const SELECTION_BAR_WIDTH = 3;
const SELECTION_KNOB = SELECTION_BAR_INSET * 2 + SELECTION_BAR_WIDTH;

export { CODE_FONT_SIZE, CODE_LINE_HEIGHT, CODE_MAX_SCALE, codeContentWidth, codeScale, gutterWidth, MARKER_WIDTH } from "./codeMetrics";

export function useCodeScale(): number {
	return codeScale(useWindowDimensions().fontScale);
}

function tokenColor(kind: SyntaxTokenKind, t: Theme): string {
	switch (kind) {
		case "comment": return t.textFaint;
		case "string": return t.green;
		case "number": return t.orange;
		case "keyword": return t.amber;
		case "type":
		case "meta": return t.textSecondary;
		default: return t.textPrimary;
	}
}

function lineWord(row: LineRow): string {
	return row.kind === "add" ? "Added" : row.kind === "del" ? "Removed" : "Line";
}

export const DiffLine = memo(function DiffLine({
	row,
	index,
	language,
	wrap,
	digits,
	selected,
	commented,
	selectionActive,
	edge,
	onSelect,
	onExtend,
}: {
	/** The ends of a selection get a knob on its bar: where a drag picks the range back up. */
	edge?: "start" | "end" | "both";
	row: LineRow;
	index: number;
	language?: string;
	wrap: boolean;
	digits: number;
	selected: boolean;
	commented: boolean;
	selectionActive: boolean;
	onSelect: (index: number) => void;
	onExtend: (index: number) => void;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const scale = useCodeScale();
	const tokens = useMemo(() => row.segments ? undefined : highlightCode(row.text, language), [language, row.segments, row.text]);
	const number = row.kind === "del" ? row.oldNo : row.newNo;
	const marker = row.kind === "add" ? "+" : row.kind === "del" ? "−" : " ";
	const tint = row.kind === "add" ? t.tintGreen : row.kind === "del" ? t.tintRed : undefined;
	const strong = row.kind === "add" ? withStrongTint(t.tintGreen) : withStrongTint(t.tintRed);
	const onAction = (event: AccessibilityActionEvent) => {
		if (event.nativeEvent.actionName === "extend") onExtend(index);
		else onSelect(index);
	};
	return (
		<Pressable
			accessibilityRole="button"
			accessibilityLabel={`${lineWord(row)} ${number ?? ""}${commented ? ", has a comment" : ""}: ${row.text.trim() || "blank line"}`}
			accessibilityHint={selectionActive ? "Selects this line. Use the extend action to grow the selection." : "Selects this line to comment or ask about it. Hold and drag to select more lines."}
			accessibilityState={{ selected }}
			accessibilityActions={[{ name: "activate" }, { name: "extend", label: "Extend selection to this line" }]}
			onAccessibilityAction={onAction}
			onPress={() => { haptics.select(); onSelect(index); }}
			style={[styles.line, tint ? { backgroundColor: tint } : null, selected && styles.lineSelected]}
		>
			<View style={[styles.gutter, { width: gutterWidth(digits, scale) }]}>
				<Text maxFontSizeMultiplier={CODE_MAX_SCALE} style={styles.gutterText}>{number ?? ""}</Text>
			</View>
			<Text maxFontSizeMultiplier={CODE_MAX_SCALE} style={[styles.marker, { width: MARKER_WIDTH * scale, color: row.kind === "add" ? t.green : row.kind === "del" ? t.red : t.textFaint }]}>{marker}</Text>
			<Text maxFontSizeMultiplier={CODE_MAX_SCALE} numberOfLines={wrap ? undefined : 1} style={[styles.code, wrap ? styles.codeWrap : null]}>
				{row.segments
					? row.segments.map((segment, at) => (
						<Text key={at} style={segment.changed ? { backgroundColor: strong } : undefined}>{segment.text}</Text>
					))
					: tokens
						? tokens.map((token, at) => <Text key={at} style={{ color: tokenColor(token.kind, t) }}>{token.text}</Text>)
						: row.text || " "}
			</Text>
			{/* The selection's edge, drawn over the gutter rather than as a border
			    that would push the line over; its knobs sit inside the row, clear
			    of the screen's edge and of the rows around it. */}
			{selected ? <View pointerEvents="none" style={styles.selectionBar} /> : null}
			{edge === "start" || edge === "both" ? <View pointerEvents="none" style={[styles.knob, styles.knobTop]} /> : null}
			{edge === "end" || edge === "both" ? <View pointerEvents="none" style={[styles.knob, styles.knobBottom]} /> : null}
		</Pressable>
	);
});

/** The word-level highlight: the line's own tint, roughly doubled. */
function withStrongTint(tint: string): string {
	const rgba = /^rgba\((\d+),\s*(\d+),\s*(\d+),\s*([\d.]+)\)$/.exec(tint);
	if (rgba) return `rgba(${rgba[1]}, ${rgba[2]}, ${rgba[3]}, ${Math.min(0.6, Number(rgba[4]) * 2.4)})`;
	const hex = /^#([0-9a-f]{6})([0-9a-f]{2})$/i.exec(tint);
	if (hex) return `#${hex[1]}${Math.min(0x99, parseInt(hex[2], 16) * 2 + 0x20).toString(16).padStart(2, "0")}`;
	return tint;
}

export const FoldRow = memo(function FoldRow({ hidden, section, expandable, onExpand }: { hidden: number; section?: string; expandable: boolean; onExpand: () => void }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const feedback = usePressFeedback("row");
	const step = Math.min(hidden, 20);
	const label = expandable
		? hidden > step ? `Show ${step} of ${plural(hidden, "hidden line")}` : `Show ${plural(hidden, "hidden line")}`
		: `${plural(hidden, "unchanged line")}`;
	return (
		<Pressable
			disabled={!expandable}
			accessibilityRole={expandable ? "button" : "text"}
			accessibilityLabel={section ? `${label}, ${section}` : label}
			onPress={onExpand}
			android_ripple={expandable ? feedback.android_ripple : undefined}
			style={({ pressed }) => [styles.fold, pressed && feedback.pressed]}
		>
			<AppSymbol name={expandable ? "chevronUpDown" : "moreHorizontal"} size={iconSize.sm} color={t.textTertiary} />
			<Text maxFontSizeMultiplier={fontScaleCap.chrome} numberOfLines={1} style={styles.foldText}>
				{label}
				{section ? <Text style={styles.foldSection}>{`  ${section}`}</Text> : null}
			</Text>
		</Pressable>
	);
});

// ---- Selection bar + comment composer ---------------------------------------------

export type ComposerAction = "comment" | "send";

export function SelectionComposer({
	label,
	busy,
	error,
	initialText,
	onAction,
	onCopy,
	onClose,
	onRise,
}: {
	label: string;
	busy: boolean;
	error?: string;
	initialText?: string;
	onAction: (action: ComposerAction, text: string) => void;
	/** Copies what the comment is about; without it there is no copy button. */
	onCopy?: () => void;
	onClose: () => void;
	/**
	 * How far its top stands above the bottom of the screen, the keyboard's lift
	 * included: where whatever has to sit above it, the page's toasts, starts.
	 */
	onRise?: (rise: number) => void;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const insets = useSafeAreaInsets();
	const iconFeedback = usePressFeedback("icon");
	const sendFeedback = usePressFeedback("button", { tone: "onAccent" });
	// Extending the range keeps what was typed; a new selection remounts the composer.
	const [text, setText] = useState(initialText ?? "");
	const trimmed = text.trim();
	const ready = Boolean(trimmed) && !busy;
	const restingInset = dockRestingInset(insets.bottom);
	const [height, setHeight] = useState(0);
	// What the sticky view below translates the dock by with the keyboard open.
	// A transform moves it without a layout pass, so its own height never says.
	const keyboard = useKeyboardState((state) => state.isVisible ? state.height : 0);
	const lift = keyboard > 0 ? Math.max(0, keyboard - restingInset + KEYBOARD_DOCK_GAP) : 0;
	useEffect(() => {
		onRise?.(height + lift);
	}, [height, lift, onRise]);
	return (
		// The session dock's spacing: it rests on the safe-area inset and, with the
		// keyboard up, sits KEYBOARD_DOCK_GAP above it instead.
		<KeyboardStickyView offset={{ closed: 0, opened: restingInset - KEYBOARD_DOCK_GAP }} onLayout={(event) => setHeight(event.nativeEvent.layout.height)} style={styles.composerDock}>
			<View accessibilityViewIsModal={false} style={[styles.composerShell, { paddingBottom: restingInset }]}>
				{/* What the comment is about, where the session composer keeps its model row. */}
				<View style={styles.composerMeta}>
					<AppSymbol name="chat" size={iconSize.sm} color={t.textTertiary} />
					<Text accessibilityRole="header" maxFontSizeMultiplier={fontScaleCap.chrome} numberOfLines={1} style={styles.composerLabel}>{label}</Text>
					{onCopy ? (
						<Pressable accessibilityRole="button" accessibilityLabel="Copy selected lines" onPress={() => { haptics.success(); onCopy(); }} android_ripple={iconFeedback.android_ripple} style={({ pressed }) => [styles.composerIcon, pressed && iconFeedback.pressed]}>
							<AppSymbol name="copy" size={iconSize.md} color={t.textSecondary} />
						</Pressable>
					) : null}
					<Pressable accessibilityRole="button" accessibilityLabel="Clear selection" onPress={onClose} android_ripple={iconFeedback.android_ripple} style={({ pressed }) => [styles.composerIcon, pressed && iconFeedback.pressed]}>
						<AppSymbol name="close" size={iconSize.md} color={t.textSecondary} />
					</Pressable>
				</View>
				{error ? <Text accessibilityRole="alert" style={styles.composerError}>{error}</Text> : null}
				{/* The session composer's capsule: same radius, same glass, controls at the touch target. */}
				<View style={styles.composerPill}>
					<ComposerGlass radius={COMPOSER_RADIUS} />
					<View style={styles.composerRow}>
						<TextInput
							value={text}
							onChangeText={setText}
							multiline
							editable={!busy}
							placeholder="Comment for the agent…"
							{...textSelectionColors(t)}
							placeholderTextColor={t.textFaint}
							accessibilityLabel={`Comment on ${label}`}
							style={styles.composerInput}
							maxFontSizeMultiplier={fontScaleCap.body}
						/>
						<Pressable
							accessibilityRole="button"
							accessibilityLabel="Add comment to review"
							accessibilityHint="Queues the comment so several can be sent together"
							accessibilityState={{ disabled: !ready }}
							disabled={!ready}
							onPress={() => onAction("comment", text)}
							android_ripple={iconFeedback.android_ripple}
							style={({ pressed }) => [styles.composerPlain, !ready && styles.disabled, pressed && iconFeedback.pressed]}
						>
							<AppSymbol name={{ ios: "plus.bubble", android: "add_comment" }} size={iconSize.lg} color={t.textSecondary} />
						</Pressable>
						<Pressable
							accessibilityRole="button"
							accessibilityLabel="Send comment now"
							accessibilityState={{ disabled: !ready, busy }}
							disabled={!ready}
							onPress={() => onAction("send", text)}
							android_ripple={sendFeedback.android_ripple}
							style={({ pressed }) => [styles.composerSend, !trimmed && !busy && styles.disabled, pressed && sendFeedback.pressed]}
						>
							{/* The accent fill's own ink: the page colour vanished on a near-white accent. */}
							{busy ? <Spinner color={t.onAccent} /> : <AppSymbol name="send" size={iconSize.md} color={t.onAccent} weight="semibold" />}
						</Pressable>
					</View>
				</View>
			</View>
		</KeyboardStickyView>
	);
}

// ---- Inline comment -----------------------------------------------------------------

/** The comment card's remove control; its hit area is grown to the touch target. */
const INLINE_ACTION = 28;

/**
 * A comment under the line it is about, the way a GitHub review thread sits in
 * a diff: the app's card, a quiet byline, the words. A queued comment can be
 * removed before it goes; a sent one stays as the record of what was asked here
 * and can be dismissed.
 */
export const InlineComment = memo(function InlineComment({ comment, digits, onRemove }: { comment: ReviewComment; digits: number; onRemove: (id: string) => void }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const scale = useCodeScale();
	const feedback = usePressFeedback("icon");
	const sent = comment.status === "sent";
	const range = comment.label.split(" · ").pop() ?? comment.label;
	return (
		<View style={[styles.inlineWrap, { paddingLeft: gutterWidth(digits, scale) + MARKER_WIDTH * scale }]}>
			<View accessible accessibilityLabel={`Your comment on ${range}, ${sent ? "sent to the agent" : "pending, not sent yet"}: ${comment.feedback}`} style={styles.inlineCard}>
				<View style={styles.inlineHead}>
					<View style={styles.inlineAvatar}>
						<AppSymbol name={{ ios: "person.fill", android: "person" }} size={iconSize.xs} color={t.textSecondary} />
					</View>
					<Text maxFontSizeMultiplier={fontScaleCap.chrome} style={styles.inlineAuthor}>You</Text>
					<Text maxFontSizeMultiplier={fontScaleCap.chrome} numberOfLines={1} style={styles.inlineMeta}>{range}</Text>
					{sent ? <AppSymbol name="checkmark" size={iconSize.xs} color={t.green} weight="semibold" /> : null}
					<Text maxFontSizeMultiplier={fontScaleCap.chrome} style={[styles.inlineState, sent && { color: t.green }]}>{sent ? "Sent" : "Pending"}</Text>
					<Pressable
						accessibilityRole="button"
						accessibilityLabel={sent ? `Dismiss comment on ${range}` : `Delete comment on ${range}`}
						hitSlop={(touchTarget - INLINE_ACTION) / 2}
						onPress={() => onRemove(comment.id)}
						android_ripple={feedback.android_ripple}
						style={({ pressed }) => [styles.inlineAction, pressed && feedback.pressed]}
					>
						<AppSymbol name={sent ? "close" : "delete"} size={iconSize.sm} color={t.textTertiary} />
					</Pressable>
				</View>
				<Text selectable maxFontSizeMultiplier={fontScaleCap.body} style={styles.inlineText}>{comment.feedback}</Text>
			</View>
		</View>
	);
});

// ---- Styles ------------------------------------------------------------------------

const makeStyles = (t: Theme) => StyleSheet.create({
	typeIcon: { width: FILE_ICON_SLOT, height: FILE_ICON_SLOT, alignItems: "center", justifyContent: "center" },
	statusLetter: { position: "absolute", right: -2, bottom: -2, fontFamily: t.fontMono, fontSize: 10, lineHeight: 12, fontWeight: "700" },
	counts: { fontFamily: t.fontMono, fontSize: type.footnote.fontSize, color: t.textSecondary },


	line: { flexDirection: "row", alignItems: "flex-start", minHeight: CODE_LINE_HEIGHT, paddingRight: space.lg },
	lineSelected: { backgroundColor: t.accentTint },
	selectionBar: { position: "absolute", left: SELECTION_BAR_INSET, top: 0, bottom: 0, width: SELECTION_BAR_WIDTH, backgroundColor: t.accent },
	gutter: { flexDirection: "row", justifyContent: "flex-end", alignItems: "center", paddingRight: space.xs, minHeight: CODE_LINE_HEIGHT },
	gutterText: { fontFamily: t.fontMono, fontSize: CODE_FONT_SIZE, lineHeight: CODE_LINE_HEIGHT, color: t.textFaint },
	knob: { position: "absolute", left: 0, width: SELECTION_KNOB, height: SELECTION_KNOB, borderRadius: SELECTION_KNOB / 2, backgroundColor: t.accent },
	knobTop: { top: 0 },
	knobBottom: { bottom: 0 },
	marker: { textAlign: "center", fontFamily: t.fontMono, fontSize: CODE_FONT_SIZE, lineHeight: CODE_LINE_HEIGHT },
	code: { fontFamily: t.fontMono, fontSize: CODE_FONT_SIZE, lineHeight: CODE_LINE_HEIGHT, color: t.textPrimary },
	codeWrap: { flex: 1, flexShrink: 1 },

	fold: { minHeight: touchTarget, flexDirection: "row", alignItems: "center", gap: space.sm, paddingHorizontal: space.md, backgroundColor: t.bgSubtle, borderTopWidth: rowDividerWidth, borderBottomWidth: rowDividerWidth, borderColor: t.borderSubtle },
	foldText: { flex: 1, ...type.footnote, fontFamily: font.medium, color: t.textSecondary },
	foldSection: { fontFamily: t.fontMono, color: t.textTertiary },

	composerDock: { position: "absolute", left: 0, right: 0, bottom: 0 },
	// ChatComposer's dock and pill, sized from the same constants, so commenting
	// on a line feels like messaging the session.
	composerShell: { backgroundColor: t.bgBase, paddingHorizontal: space.md, paddingTop: space.xxs, gap: space.xxs },
	// The session composer's meta row: its left edge, and the close button over send.
	composerMeta: { minHeight: touchTarget, flexDirection: "row", alignItems: "center", gap: space.sm, paddingLeft: space.sm, paddingRight: space.xs },
	composerLabel: { flex: 1, fontFamily: font.semibold, fontSize: type.footnote.fontSize, fontWeight: "600", color: t.textSecondary },
	composerIcon: { width: touchTarget, height: touchTarget, alignItems: "center", justifyContent: "center", marginLeft: -space.sm },
	composerError: { ...type.footnote, color: t.red, paddingHorizontal: space.sm },
	composerPill: { minHeight: COMPOSER_HEIGHT, padding: space.xs, backgroundColor: composerGlassSupported ? "transparent" : t.bgElevated, borderRadius: COMPOSER_RADIUS, borderCurve: "continuous" },
	composerRow: { flexDirection: "row", alignItems: "flex-end", gap: space.xxs },
	composerInput: { flex: 1, minHeight: COMPOSER_FIELD_HEIGHT, maxHeight: COMPOSER_FIELD_MAX_HEIGHT, fontFamily: font.regular, fontSize: type.subheadline.fontSize, lineHeight: type.subheadline.lineHeight, color: t.textPrimary, paddingHorizontal: space.md, paddingVertical: space.md, textAlignVertical: "top" },
	composerPlain: { width: touchTarget, height: touchTarget, borderRadius: touchTarget / 2, alignItems: "center", justifyContent: "center" },
	composerSend: { width: touchTarget, height: touchTarget, borderRadius: touchTarget / 2, alignItems: "center", justifyContent: "center", backgroundColor: t.accent },
	disabled: { opacity: stateOpacity.disabled },

	inlineWrap: { paddingRight: space.md, paddingTop: space.xs, paddingBottom: space.sm, backgroundColor: t.bgBase },
	inlineCard: { backgroundColor: t.bgElevated, borderRadius: radius.md, borderCurve: "continuous", borderWidth: rowDividerWidth, borderColor: t.borderDefault, paddingHorizontal: space.md, paddingTop: space.sm, paddingBottom: space.md, gap: space.xs },
	inlineHead: { flexDirection: "row", alignItems: "center", gap: space.xs },
	inlineAvatar: { width: 20, height: 20, borderRadius: 10, alignItems: "center", justifyContent: "center", backgroundColor: t.bgSubtle },
	inlineAuthor: { fontFamily: font.semibold, fontSize: type.footnote.fontSize, color: t.textPrimary },
	inlineMeta: { flex: 1, fontFamily: font.regular, fontSize: type.footnote.fontSize, color: t.textTertiary },
	inlineState: { ...type.footnote, fontFamily: font.medium, color: t.textTertiary },
	inlineAction: { width: INLINE_ACTION, height: INLINE_ACTION, borderRadius: INLINE_ACTION / 2, marginRight: -space.xs, alignItems: "center", justifyContent: "center" },
	inlineText: { ...type.subheadline, color: t.textPrimary, paddingLeft: 20 + space.xs },

});
