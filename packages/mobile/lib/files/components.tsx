import { memo, useEffect, useMemo, useState } from "react";
import {
	ActivityIndicator,
	FlatList,
	Modal,
	Platform,
	Pressable,
	StyleSheet,
	Text,
	TextInput,
	View,
	type AccessibilityActionEvent,
} from "react-native";
import { KeyboardStickyView } from "react-native-keyboard-controller";
import { SvgXml } from "react-native-svg";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { ComposerGlass, composerGlassSupported } from "../chat/composer-glass";
import { dockRestingInset, KEYBOARD_DOCK_GAP } from "../session/keyboardInset";
import { highlightCode, type SyntaxTokenKind } from "../chat/syntaxHighlight";
import { haptics } from "../haptics";
import { Feather, type FeatherIconName } from "../icons";
import type { Theme } from "../theme";
import { textSelectionColors } from "../textSelection";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { font, fontScaleCap, iconSize, press, radius, space, touchTarget, type } from "../tokens";
import type { WorkspaceFileStatus } from "./api";
import { fileIconSvg, folderIconSvg } from "./fileTypeIcon";
import { changeAccessibilityLabel, plural, sectionLabel, splitPath, statusLabel, type ChangeEntry, type DisplayRow, type LineRow } from "./model";
import type { ReviewComment } from "./reviewStore";

// ---- Segmented tabs -----------------------------------------------------------

export function SegmentedTabs<K extends string>({ value, options, onChange }: {
	value: K;
	options: Array<{ key: K; label: string; icon?: FeatherIconName; badge?: number }>;
	onChange: (key: K) => void;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	return (
		<View accessibilityRole="tablist" style={styles.segmented}>
			{options.map((option) => {
				const selected = option.key === value;
				return (
					<Pressable
						key={option.key}
						accessibilityRole="tab"
						accessibilityState={{ selected }}
						accessibilityLabel={option.badge ? `${option.label}, ${option.badge}` : option.label}
						onPress={() => { if (!selected) { haptics.select(); onChange(option.key); } }}
						style={({ pressed }) => [styles.segment, selected && styles.segmentOn, pressed && !selected && { opacity: press.opacity }]}
					>
						{option.icon ? <Feather name={option.icon} size={iconSize.sm} color={selected ? t.textPrimary : t.textTertiary} /> : null}
						<Text maxFontSizeMultiplier={fontScaleCap.chrome} style={[styles.segmentLabel, selected && styles.segmentLabelOn]}>{option.label}</Text>
						{option.badge ? <Text maxFontSizeMultiplier={fontScaleCap.chrome} style={styles.segmentBadge}>{option.badge}</Text> : null}
					</Pressable>
				);
			})}
		</View>
	);
}

// ---- Status + counts ------------------------------------------------------------

export function statusColor(status: WorkspaceFileStatus, t: Theme): string {
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

/**
 * A file as editors show it: the logo of its type (the same Symbols icons
 * desktop draws), with desktop's status letter — A, M, D, R in its colour — at
 * the bottom right. Decorative: every row that uses it already names the file
 * and its status.
 */
export const FileTypeIcon = memo(function FileTypeIcon({ path, status, size = 22 }: { path: string; status?: WorkspaceFileStatus; size?: number }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const xml = useMemo(() => fileIconSvg(path), [path]);
	const badge = status && status !== "unmodified" ? status : undefined;
	return (
		<View accessible={false} importantForAccessibility="no-hide-descendants" style={[styles.typeIcon, { width: size + 6, height: size + 4 }]}>
			<SvgXml xml={xml} width={size} height={size} opacity={status === "deleted" ? 0.55 : 1} />
			{badge ? <Text maxFontSizeMultiplier={1} style={[styles.statusLetter, { color: statusColor(badge, t) }]}>{STATUS_LETTER[badge]}</Text> : null}
		</View>
	);
});

export const FolderTypeIcon = memo(function FolderTypeIcon({ name, size = 22 }: { name: string; size?: number }) {
	const styles = useThemedStyles(makeStyles);
	const xml = useMemo(() => folderIconSvg(name), [name]);
	return (
		<View accessible={false} importantForAccessibility="no-hide-descendants" style={[styles.typeIcon, { width: size + 4, height: size + 4 }]}>
			<SvgXml xml={xml} width={size} height={size} />
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

// ---- Changed-file row -------------------------------------------------------------

export const ChangeRow = memo(function ChangeRow({ file, comments, onPress }: { file: ChangeEntry; comments: number; onPress: (file: ChangeEntry) => void }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const { name, dir } = splitPath(file.path);
	const badges = file.sections.filter((section) => section !== "unstaged" && section !== "committed").map((section) => sectionLabel[section]);
	const detail = [file.previousPath ? `from ${file.previousPath}` : dir, ...badges].filter(Boolean).join(" · ");
	return (
		<Pressable
			accessibilityRole="button"
			accessibilityLabel={`${changeAccessibilityLabel(file)}${comments ? `, ${plural(comments, "queued comment")}` : ""}`}
			accessibilityHint="Opens the diff"
			onPress={() => { haptics.tap(); onPress(file); }}
			style={({ pressed }) => [styles.fileRow, pressed && { backgroundColor: t.bgElevatedHover }]}
		>
			<FileTypeIcon path={file.path} status={file.status} />
			<View style={styles.fileRowText}>
				<Text numberOfLines={1} maxFontSizeMultiplier={fontScaleCap.body} style={[styles.fileName, file.status === "deleted" && styles.fileNameDeleted]}>{name}</Text>
				{detail ? <Text numberOfLines={1} ellipsizeMode="head" maxFontSizeMultiplier={fontScaleCap.body} style={styles.fileDir}>{detail}</Text> : null}
			</View>
			{comments ? (
				<View style={styles.commentCount}>
					<Feather name="message-square" size={iconSize.xs} color={t.textSecondary} />
					<Text maxFontSizeMultiplier={fontScaleCap.chrome} style={styles.commentCountText}>{comments}</Text>
				</View>
			) : null}
			<LineCounts additions={file.additions} deletions={file.deletions} binary={file.binary} />
			<Feather name="chevron-right" size={iconSize.sm} color={t.textFaint} />
		</Pressable>
	);
});

// ---- Diff lines ---------------------------------------------------------------------

/** Monospace metrics for the code rows: fixed, so unwrapped rows can be measured without layout. */
export const CODE_FONT_SIZE = 12.5;
export const CODE_LINE_HEIGHT = 19;
const CODE_CHAR_WIDTH = CODE_FONT_SIZE * 0.6;

export function codeContentWidth(rows: DisplayRow[], gutterDigits: number): number {
	let longest = 0;
	for (const row of rows) if (row.type === "line" && row.row.text.length > longest) longest = row.row.text.length;
	return gutterWidth(gutterDigits) + MARKER_WIDTH + space.lg + Math.ceil(longest * CODE_CHAR_WIDTH);
}

const MARKER_WIDTH = 16;
export function gutterWidth(digits: number): number {
	return Math.max(2, digits) * CODE_CHAR_WIDTH + space.md;
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
	/** The ends of a selection get a knob in the gutter: where a drag picks the range back up. */
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
			{edge === "start" || edge === "both" ? <View pointerEvents="none" style={[styles.knob, styles.knobTop]} /> : null}
			{edge === "end" || edge === "both" ? <View pointerEvents="none" style={[styles.knob, styles.knobBottom]} /> : null}
			<View style={[styles.gutter, { width: gutterWidth(digits) }]}>
				<Text maxFontSizeMultiplier={1} style={styles.gutterText}>{number ?? ""}</Text>
			</View>
			<Text maxFontSizeMultiplier={1} style={[styles.marker, { color: row.kind === "add" ? t.green : row.kind === "del" ? t.red : t.textFaint }]}>{marker}</Text>
			<Text maxFontSizeMultiplier={1.4} numberOfLines={wrap ? undefined : 1} style={[styles.code, wrap ? styles.codeWrap : null]}>
				{row.segments
					? row.segments.map((segment, at) => (
						<Text key={at} style={segment.changed ? { backgroundColor: strong } : undefined}>{segment.text}</Text>
					))
					: tokens
						? tokens.map((token, at) => <Text key={at} style={{ color: tokenColor(token.kind, t) }}>{token.text}</Text>)
						: row.text || " "}
			</Text>
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
	const step = Math.min(hidden, 20);
	const label = expandable
		? hidden > step ? `Show ${step} of ${plural(hidden, "hidden line")}` : `Show ${plural(hidden, "hidden line")}`
		: `${plural(hidden, "unchanged line")}`;
	return (
		<Pressable
			disabled={!expandable}
			accessibilityRole={expandable ? "button" : "text"}
			accessibilityLabel={section ? `${label}, ${section}` : label}
			onPress={() => { haptics.tap(); onExpand(); }}
			style={({ pressed }) => [styles.fold, pressed && { opacity: press.opacity }]}
		>
			<Feather name={expandable ? "unfold-vertical" : "more-horizontal"} size={iconSize.sm} color={t.textTertiary} />
			<Text maxFontSizeMultiplier={fontScaleCap.chrome} numberOfLines={1} style={styles.foldText}>
				{label}
				{section ? <Text style={styles.foldSection}>{`  ${section}`}</Text> : null}
			</Text>
		</Pressable>
	);
});

// ---- Selection bar + comment composer ---------------------------------------------

export type ComposerAction = "comment" | "send";

/** The session composer's capsule corner (ChatComposer), so the glass behind both matches. */
const COMPOSER_RADIUS = 28;

export function SelectionComposer({
	label,
	busy,
	error,
	initialText,
	onAction,
	onCopy,
	onClose,
}: {
	label: string;
	busy: boolean;
	error?: string;
	initialText?: string;
	onAction: (action: ComposerAction, text: string) => void;
	onCopy: () => void;
	onClose: () => void;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const insets = useSafeAreaInsets();
	// Extending the range keeps what was typed; a new selection remounts the composer.
	const [text, setText] = useState(initialText ?? "");
	const trimmed = text.trim();
	const ready = Boolean(trimmed) && !busy;
	const restingInset = dockRestingInset(insets.bottom);
	return (
		// The session dock's spacing: it rests on the safe-area inset and, with the
		// keyboard up, sits KEYBOARD_DOCK_GAP above it instead.
		<KeyboardStickyView offset={{ closed: 0, opened: restingInset - KEYBOARD_DOCK_GAP }} style={styles.composerDock}>
			<View accessibilityViewIsModal={false} style={[styles.composerShell, { paddingBottom: restingInset }]}>
				{/* What the comment is about, where the session composer keeps its model row. */}
				<View style={styles.composerMeta}>
					<Feather name="message-square-text" size={iconSize.sm} color={t.textTertiary} />
					<Text accessibilityRole="header" maxFontSizeMultiplier={fontScaleCap.chrome} numberOfLines={1} style={styles.composerLabel}>{label}</Text>
					<Pressable accessibilityRole="button" accessibilityLabel="Copy selected lines" hitSlop={6} onPress={() => { haptics.success(); onCopy(); }} style={({ pressed }) => [styles.composerIcon, pressed && { opacity: press.opacity }]}>
						<Feather name="copy" size={iconSize.md} color={t.textSecondary} />
					</Pressable>
					<Pressable accessibilityRole="button" accessibilityLabel="Clear selection" hitSlop={6} onPress={() => { haptics.tap(); onClose(); }} style={({ pressed }) => [styles.composerIcon, pressed && { opacity: press.opacity }]}>
						<Feather name="x" size={iconSize.md} color={t.textSecondary} />
					</Pressable>
				</View>
				{error ? <Text accessibilityRole="alert" style={styles.composerError}>{error}</Text> : null}
				{/* The session composer's capsule: same radius, same glass, same 44-point controls. */}
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
							onPress={() => { haptics.tap(); onAction("comment", text); }}
							style={({ pressed }) => [styles.composerPlain, !ready && styles.disabled, pressed && { opacity: press.opacity }]}
						>
							<Feather name="message-square-plus" size={iconSize.lg} color={t.textSecondary} />
						</Pressable>
						<Pressable
							accessibilityRole="button"
							accessibilityLabel="Send comment now"
							accessibilityState={{ disabled: !ready, busy }}
							disabled={!ready}
							onPress={() => { haptics.tap(); onAction("send", text); }}
							style={({ pressed }) => [styles.composerSend, !trimmed && !busy && { opacity: 0.35 }, pressed && { opacity: 0.8 }]}
						>
							{busy ? <ActivityIndicator size="small" color={t.bgBase} /> : <Feather name="arrow-up" size={17} color={t.bgBase} />}
						</Pressable>
					</View>
				</View>
			</View>
		</KeyboardStickyView>
	);
}

// ---- Inline comment -----------------------------------------------------------------

/**
 * A comment under the line it is about, the way a GitHub review thread sits in
 * a diff: the app's card, a quiet byline, the words. A queued comment can be
 * removed before it goes; a sent one stays as the record of what was asked here
 * and can be dismissed.
 */
export const InlineComment = memo(function InlineComment({ comment, digits, onRemove }: { comment: ReviewComment; digits: number; onRemove: (id: string) => void }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const sent = comment.status === "sent";
	const range = comment.label.split(" · ").pop() ?? comment.label;
	return (
		<View style={[styles.inlineWrap, { paddingLeft: gutterWidth(digits) + MARKER_WIDTH }]}>
			<View accessible accessibilityLabel={`Your comment on ${range}, ${sent ? "sent to the agent" : "pending, not sent yet"}: ${comment.feedback}`} style={styles.inlineCard}>
				<View style={styles.inlineHead}>
					<View style={styles.inlineAvatar}>
						<Feather name="user" size={11} color={t.textSecondary} />
					</View>
					<Text maxFontSizeMultiplier={fontScaleCap.chrome} style={styles.inlineAuthor}>You</Text>
					<Text maxFontSizeMultiplier={fontScaleCap.chrome} numberOfLines={1} style={styles.inlineMeta}>{range}</Text>
					{sent ? <Feather name="check" size={12} color={t.green} /> : null}
					<Text maxFontSizeMultiplier={fontScaleCap.chrome} style={[styles.inlineState, sent && { color: t.green }]}>{sent ? "Sent" : "Pending"}</Text>
					<Pressable
						accessibilityRole="button"
						accessibilityLabel={sent ? `Dismiss comment on ${range}` : `Delete comment on ${range}`}
						hitSlop={12}
						onPress={() => { haptics.tap(); onRemove(comment.id); }}
						style={({ pressed }) => [styles.inlineAction, pressed && { opacity: press.opacity }]}
					>
						<Feather name={sent ? "x" : "trash-2"} size={iconSize.sm} color={t.textTertiary} />
					</Pressable>
				</View>
				<Text selectable maxFontSizeMultiplier={fontScaleCap.body} style={styles.inlineText}>{comment.feedback}</Text>
			</View>
		</View>
	);
});

// ---- Review tray ---------------------------------------------------------------------

/**
 * The floating "Send N comments" pill and the sheet behind it. It stays above
 * the content without covering the line being reviewed, the way GitHub's
 * Finish Review pill does, and is reachable from every Files screen.
 */
export function ReviewTray({
	comments,
	sending,
	error,
	bottomOffset = 0,
	onSend,
	onRemove,
}: {
	comments: readonly ReviewComment[];
	sending: boolean;
	error?: string;
	bottomOffset?: number;
	onSend: () => void;
	onRemove: (id: string) => void;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const insets = useSafeAreaInsets();
	const [open, setOpen] = useState(false);
	useEffect(() => { if (!comments.length) setOpen(false); }, [comments.length]);
	if (!comments.length) return null;
	const count = plural(comments.length, "comment");
	return (
		<>
			<View pointerEvents="box-none" style={[styles.trayDock, { bottom: Math.max(insets.bottom, space.md) + bottomOffset }]}>
				<Pressable
					accessibilityRole="button"
					accessibilityLabel={`Review ${count} before sending`}
					onPress={() => { haptics.tap(); setOpen(true); }}
					style={({ pressed }) => [styles.trayPill, pressed && { opacity: press.opacity }]}
				>
					<Feather name="message-square" size={iconSize.sm} color={t.onAccent} />
					<Text maxFontSizeMultiplier={fontScaleCap.chrome} style={styles.trayPillText}>{`Send ${count}`}</Text>
				</Pressable>
			</View>
			<Modal visible={open} animationType="slide" presentationStyle={Platform.OS === "ios" ? "pageSheet" : undefined} onRequestClose={() => setOpen(false)}>
				{/* Android draws the modal edge to edge, under the status bar; iOS's page sheet starts below it. */}
				<View style={[styles.sheet, { paddingTop: Platform.OS === "android" ? insets.top + space.lg : space.xl, paddingBottom: Math.max(insets.bottom, space.lg) }]}>
					<View style={styles.sheetHead}>
						<Text accessibilityRole="header" maxFontSizeMultiplier={fontScaleCap.title} style={styles.sheetTitle}>Review comments</Text>
						<Pressable accessibilityRole="button" accessibilityLabel="Close" hitSlop={10} onPress={() => setOpen(false)} style={styles.composerIcon}>
							<Feather name="x" size={iconSize.lg} color={t.textSecondary} />
						</Pressable>
					</View>
					<Text maxFontSizeMultiplier={fontScaleCap.body} style={styles.sheetLead}>Sent to the agent as one message, with the file and line for each.</Text>
					<FlatList
						data={comments}
						keyExtractor={(comment) => comment.id}
						contentContainerStyle={{ paddingBottom: space.lg }}
						renderItem={({ item }) => (
							<View style={styles.queued}>
								<View style={{ flex: 1 }}>
									<Text maxFontSizeMultiplier={fontScaleCap.chrome} numberOfLines={1} style={styles.queuedLabel}>{item.label}</Text>
									<Text maxFontSizeMultiplier={fontScaleCap.body} style={styles.queuedText}>{item.feedback}</Text>
								</View>
								<Pressable accessibilityRole="button" accessibilityLabel={`Remove comment on ${item.label}`} hitSlop={8} onPress={() => { haptics.tap(); onRemove(item.id); }} style={styles.composerIcon}>
									<Feather name="trash-2" size={iconSize.md} color={t.textTertiary} />
								</Pressable>
							</View>
						)}
					/>
					{error ? <Text accessibilityRole="alert" style={styles.composerError}>{error}</Text> : null}
					<Pressable
						accessibilityRole="button"
						accessibilityLabel={`Send ${count} to the agent`}
						accessibilityState={{ busy: sending, disabled: sending }}
						disabled={sending}
						onPress={() => { haptics.tap(); onSend(); }}
						style={({ pressed }) => [styles.sheetSend, sending && styles.disabled, pressed && { opacity: press.opacity }]}
					>
						{sending ? <ActivityIndicator size="small" color={t.onAccent} /> : <Feather name="send" size={iconSize.md} color={t.onAccent} />}
						<Text maxFontSizeMultiplier={fontScaleCap.body} style={styles.sheetSendText}>{`Send ${count}`}</Text>
					</Pressable>
				</View>
			</Modal>
		</>
	);
}

// ---- Styles ------------------------------------------------------------------------

const makeStyles = (t: Theme) => StyleSheet.create({
	segmented: { flexDirection: "row", padding: space.hair, borderRadius: radius.sm + space.hair, backgroundColor: t.bgElevated, borderWidth: StyleSheet.hairlineWidth, borderColor: t.borderSubtle },
	segment: { flex: 1, minHeight: 34, flexDirection: "row", alignItems: "center", justifyContent: "center", gap: space.xs, borderRadius: radius.sm, paddingHorizontal: space.sm },
	segmentOn: { backgroundColor: t.bgElevatedHover, borderWidth: StyleSheet.hairlineWidth, borderColor: t.borderDefault },
	segmentLabel: { fontFamily: font.medium, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, color: t.textTertiary },
	segmentLabelOn: { color: t.textPrimary },
	segmentBadge: { fontFamily: font.medium, fontSize: type.caption2.fontSize, color: t.textSecondary, backgroundColor: t.bgSubtle, borderRadius: radius.pill, paddingHorizontal: space.xs, overflow: "hidden" },

	typeIcon: { alignItems: "center", justifyContent: "center" },
	statusLetter: { position: "absolute", right: -3, bottom: -3, fontFamily: t.fontMono, fontSize: 10, lineHeight: 12, fontWeight: "700" },
	counts: { fontFamily: t.fontMono, fontSize: type.caption1.fontSize, color: t.textSecondary },

	fileRow: { minHeight: touchTarget + space.sm, flexDirection: "row", alignItems: "center", gap: space.md, paddingHorizontal: space.lg, paddingVertical: space.sm },
	fileRowText: { flex: 1, minWidth: 0 },
	fileName: { ...type.subheadline, fontFamily: font.medium, color: t.textPrimary },
	fileNameDeleted: { color: t.textSecondary, textDecorationLine: "line-through" },
	fileDir: { ...type.footnote, color: t.textTertiary },
	commentCount: { flexDirection: "row", alignItems: "center", gap: space.hair },
	commentCountText: { fontFamily: font.medium, fontSize: type.caption2.fontSize, color: t.textSecondary },

	line: { flexDirection: "row", alignItems: "flex-start", minHeight: CODE_LINE_HEIGHT, paddingRight: space.lg },
	lineSelected: { backgroundColor: t.accentTint, borderLeftWidth: 3, borderLeftColor: t.accent },
	gutter: { flexDirection: "row", justifyContent: "flex-end", alignItems: "center", paddingRight: space.xs, minHeight: CODE_LINE_HEIGHT },
	gutterText: { fontFamily: t.fontMono, fontSize: CODE_FONT_SIZE - 1.5, lineHeight: CODE_LINE_HEIGHT, color: t.textFaint },
	knob: { position: "absolute", left: -4, width: 9, height: 9, borderRadius: 5, backgroundColor: t.accent, zIndex: 1 },
	knobTop: { top: -4 },
	knobBottom: { bottom: -4 },
	marker: { width: MARKER_WIDTH, textAlign: "center", fontFamily: t.fontMono, fontSize: CODE_FONT_SIZE, lineHeight: CODE_LINE_HEIGHT },
	code: { fontFamily: t.fontMono, fontSize: CODE_FONT_SIZE, lineHeight: CODE_LINE_HEIGHT, color: t.textPrimary },
	codeWrap: { flex: 1, flexShrink: 1 },

	fold: { minHeight: touchTarget - space.sm, flexDirection: "row", alignItems: "center", gap: space.sm, paddingHorizontal: space.md, backgroundColor: t.bgSubtle, borderTopWidth: StyleSheet.hairlineWidth, borderBottomWidth: StyleSheet.hairlineWidth, borderColor: t.borderSubtle },
	foldText: { flex: 1, fontFamily: font.medium, fontSize: type.caption1.fontSize, color: t.textSecondary },
	foldSection: { fontFamily: t.fontMono, color: t.textTertiary },

	composerDock: { position: "absolute", left: 0, right: 0, bottom: 0 },
	// Mirrors ChatComposer's dock and pill (COMPOSER_HEIGHT 56, field 44, radius 28),
	// so commenting on a line feels like messaging the session.
	composerShell: { backgroundColor: t.bgBase, paddingHorizontal: space.md, paddingTop: space.xxs, gap: space.xxs },
	// The session composer's meta row: its left edge, and the close button over send.
	composerMeta: { height: 44, flexDirection: "row", alignItems: "center", gap: space.sm, paddingLeft: space.sm, paddingRight: space.xs },
	composerLabel: { flex: 1, fontFamily: font.semibold, fontSize: type.footnote.fontSize, fontWeight: "600", color: t.textSecondary },
	composerIcon: { width: 44, height: 44, alignItems: "center", justifyContent: "center", marginLeft: -space.sm },
	composerError: { ...type.footnote, color: t.red, paddingHorizontal: space.sm },
	composerPill: { minHeight: 56, padding: space.xs, backgroundColor: composerGlassSupported ? "transparent" : t.bgElevated, borderRadius: COMPOSER_RADIUS, borderCurve: "continuous" },
	composerRow: { flexDirection: "row", alignItems: "flex-end", gap: space.xxs },
	composerInput: { flex: 1, minHeight: 44, maxHeight: 138, fontFamily: font.regular, fontSize: type.subheadline.fontSize, lineHeight: type.subheadline.lineHeight, color: t.textPrimary, paddingHorizontal: space.md, paddingVertical: space.md, textAlignVertical: "top" },
	composerPlain: { width: 44, height: 44, borderRadius: 22, alignItems: "center", justifyContent: "center" },
	composerSend: { width: 44, height: 44, borderRadius: 22, alignItems: "center", justifyContent: "center", backgroundColor: t.accent },
	disabled: { opacity: 0.45 },

	inlineWrap: { paddingRight: space.md, paddingTop: space.xs, paddingBottom: space.sm, backgroundColor: t.bgBase },
	inlineCard: { backgroundColor: t.bgElevated, borderRadius: radius.md, borderCurve: "continuous", borderWidth: StyleSheet.hairlineWidth, borderColor: t.borderDefault, paddingHorizontal: space.md, paddingTop: space.sm, paddingBottom: space.md, gap: space.xs },
	inlineHead: { flexDirection: "row", alignItems: "center", gap: space.xs },
	inlineAvatar: { width: 20, height: 20, borderRadius: 10, alignItems: "center", justifyContent: "center", backgroundColor: t.bgSubtle },
	inlineAuthor: { fontFamily: font.semibold, fontSize: type.footnote.fontSize, color: t.textPrimary },
	inlineMeta: { flex: 1, fontFamily: font.regular, fontSize: type.footnote.fontSize, color: t.textTertiary },
	inlineState: { fontFamily: font.medium, fontSize: type.caption1.fontSize, color: t.textTertiary },
	inlineAction: { width: 28, height: 28, marginRight: -space.xs, alignItems: "center", justifyContent: "center" },
	inlineText: { ...type.subheadline, color: t.textPrimary, paddingLeft: 20 + space.xs },
	trayDock: { position: "absolute", left: 0, right: 0, alignItems: "center" },
	trayPill: { minHeight: touchTarget, flexDirection: "row", alignItems: "center", gap: space.sm, paddingHorizontal: space.xl, borderRadius: radius.pill, backgroundColor: t.accent, shadowColor: "#000", shadowOpacity: 0.25, shadowRadius: 12, shadowOffset: { width: 0, height: 4 }, elevation: 6 },
	trayPillText: { fontFamily: font.semibold, fontSize: type.subheadline.fontSize, color: t.onAccent },

	sheet: { flex: 1, backgroundColor: t.bgBase, paddingHorizontal: space.lg },
	sheetHead: { flexDirection: "row", alignItems: "center", justifyContent: "space-between" },
	sheetTitle: { ...type.title3, color: t.textPrimary },
	sheetLead: { ...type.footnote, color: t.textTertiary, marginTop: space.xxs, marginBottom: space.md },
	queued: { flexDirection: "row", alignItems: "flex-start", gap: space.sm, paddingVertical: space.md, borderBottomWidth: StyleSheet.hairlineWidth, borderColor: t.borderSubtle },
	queuedLabel: { fontFamily: t.fontMono, fontSize: type.caption1.fontSize, color: t.textTertiary, marginBottom: space.xxs },
	queuedText: { ...type.subheadline, color: t.textPrimary },
	sheetSend: { minHeight: touchTarget + space.xxs, flexDirection: "row", alignItems: "center", justifyContent: "center", gap: space.sm, borderRadius: radius.md, backgroundColor: t.accent },
	sheetSendText: { fontFamily: font.semibold, fontSize: type.body.fontSize, color: t.onAccent },
});
