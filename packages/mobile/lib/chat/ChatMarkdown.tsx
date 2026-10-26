import { UITextView as Text } from "@bsky.app/react-native-uitextview";
import * as Clipboard from "expo-clipboard";
import { createContext, isValidElement, memo, useCallback, useContext, useEffect, useState, type ReactNode } from "react";
import { Image, Platform, Pressable, ScrollView, StyleSheet, View, type StyleProp, type TextStyle } from "react-native";
import { AppSymbol } from "../AppSymbol";
import { rowDividerWidth } from "../divider";
import { haptics } from "../haptics";
import { openGitHub } from "../openGitHub";
import { usePressFeedback } from "../pressFeedback";
import type { Theme } from "../theme";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { textSelectionColors } from "../textSelection";
import { useAndroidResponseSelection } from "./useAndroidResponseSelection";
import { FirstLineMarker } from "./FirstLineMarker";
import { HighlightedCodeText } from "./HighlightedCodeText";
import { allowsUnderscoreEmphasis } from "./inlineEmphasis";
import { parseBlocks, type MarkdownBlock } from "./markdownBlocks";
import { iconSize, prose, radius, space, touchTarget, type } from "../tokens";
import type { ChatExcerpt } from "./types";

// The conversation screen decides how a tapped link opens (it knows the AO host
// that the agent's localhost links map onto); the renderer only reports the tap.

const AddResponseToChat = createContext<((text: string, source?: Omit<ChatExcerpt, "text">) => void) | null>(null);
const OpenChatLink = createContext<((url: string) => void) | null>(null);

/**
 * What a tapped link does when the renderer is used outside a provider.
 *
 * `openGitHub` is already the whole rule for a web page — GitHub app when it has
 * a screen for it, in-app browser otherwise — so the only thing a caller loses
 * without a provider is the localhost-to-AO-host rewrite, which only the
 * conversation needs.
 *
 * This used to throw instead. `ChatMarkdown` is the app's only Markdown
 * renderer, so every new surface that shows agent or reviewer prose reaches for
 * it; a required provider turns forgetting one into a crash on the first body
 * that happens to contain a URL, which is both intermittent and invisible to
 * tsc. A default that still keeps the user inside the app is the safer contract.
 */
function defaultOpenChatLink(url: string): void {
	void openGitHub(url);
}

export function ChatLinkProvider({ onLinkOpen, onAddToChat, children }: { onLinkOpen: (url: string) => void; onAddToChat?: (text: string, source?: Omit<ChatExcerpt, "text">) => void; children: ReactNode }) {
	return <OpenChatLink.Provider value={onLinkOpen}><AddResponseToChat.Provider value={onAddToChat ?? null}>{children}</AddResponseToChat.Provider></OpenChatLink.Provider>;
}

/**
 * Small native CommonMark renderer for the conversation surface.
 *
 * It deliberately covers the structures coding agents emit constantly (code,
 * headings, lists, quotes, links and inline emphasis). Unknown Markdown remains
 * readable text; the renderer never hides content because a provider used
 * syntax it does not know.
 */
export const ChatMarkdown = memo(function ChatMarkdown({ text, streaming = false, continuousSelection = false, selectionSource }: { text: string; streaming?: boolean; continuousSelection?: boolean; selectionSource?: Omit<ChatExcerpt, "text"> }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const selectionColor = textSelectionColors(t).selectionColor;
	const blocks = parseBlocks(text);
	if (continuousSelection) {
		// Native selection cannot cross sibling views. Keep every response text block
		// in one text surface, then show image previews without repeating captions.
		const images = blocks.filter((block): block is Extract<MarkdownBlock, { kind: "image" }> => block.kind === "image");
		return <View style={styles.root}>
			<SelectableResponseText text={text} source={selectionSource} />
			{images.map((image, index) => <MarkdownImage key={`${image.url}-${index}`} alt={image.alt} url={image.url} selectionColor={selectionColor} showCaption={false} />)}
		</View>;
	}
	const content: ReactNode[] = [];
	let prose: Array<Extract<MarkdownBlock, { kind: "paragraph" | "heading" }>> = [];
	const flushProse = () => {
		if (prose.length) content.push(<SelectableProseGroup key={content.length} blocks={prose} selectionColor={selectionColor} styles={styles} />);
		prose = [];
	};
	for (const block of blocks) {
		if (block.kind === "paragraph" || block.kind === "heading") {
			// iOS needs RN Text for inline links so VoiceOver can focus them.
			// Keep those blocks separate without splitting the surrounding prose.
			if (Platform.OS === "ios" && hasInlineLink(block.text, styles)) {
				flushProse();
				content.push(<SelectableInlineText key={content.length} value={block.text} selectionColor={selectionColor} style={block.kind === "heading" ? [styles.heading, block.level > 2 && styles.smallHeading] : styles.body} styles={styles} />);
			} else prose.push(block);
			continue;
		}
		flushProse();
		const index = content.length;
		if (block.kind === "code") content.push(<CodeBlock key={index} {...block} streaming={streaming} />);
		else if (block.kind === "image") content.push(<MarkdownImage key={index} alt={block.alt} url={block.url} selectionColor={selectionColor} />);
		else if (block.kind === "table") content.push(<MarkdownTable key={index} headers={block.headers} rows={block.rows} selectionColor={selectionColor} />);
		else if (block.kind === "rule") content.push(<View key={index} style={styles.rule} />);
		else if (block.kind === "quote") content.push(<View key={index} style={styles.quote}><SelectableInlineText value={block.text} selectionColor={selectionColor} style={styles.quoteText} styles={styles} /></View>);
		else content.push(<View key={index} style={styles.list}>{block.items.map((item, itemIndex) => <View key={itemIndex} style={styles.listRow}><ListMarker checked={item.checked} number={block.ordered ? itemIndex + 1 : undefined} styles={styles} /><SelectableInlineText value={item.text} selectionColor={selectionColor} style={[styles.body, styles.listBody, item.checked && styles.taskDone]} styles={styles} /></View>)}</View>);
	}
	flushProse();
	return (
		<View style={styles.root}>{content}</View>
	);
});

// A task's box, in each platform's own checkbox glyph.
const TASK_DONE = { ios: "checkmark.square", android: "check_box" } as const;
const TASK_OPEN = { ios: "square", android: "check_box_outline_blank" } as const;

/**
 * A list item's marker beside its selectable text: a drawn bullet or checkbox,
 * or the item's number. The continuous selection surface keeps the characters
 * instead, since they are part of the text a reader copies from there.
 */
function ListMarker({ checked, number, styles }: { checked?: boolean; number?: number; styles: ReturnType<typeof makeStyles> }) {
	const t = useTheme();
	if (checked !== undefined) return <FirstLineMarker lineStyle={styles.body} style={styles.markerBox}><AppSymbol name={checked ? TASK_DONE : TASK_OPEN} size={iconSize.md} color={t.textTertiary} accessibilityLabel={checked ? "Done" : "Not done"} /></FirstLineMarker>;
	if (number !== undefined) return <Text style={styles.marker}>{`${number}.`}</Text>;
	return <FirstLineMarker lineStyle={styles.body} style={styles.markerBox}><View style={styles.bullet} /></FirstLineMarker>;
}

function hasInlineLink(value: string, styles: ReturnType<typeof makeStyles>): boolean {
	return inline(value, styles).some((node) => isValidElement(node) && node.type === MarkdownLink);
}

function SelectableProseGroup({ blocks, selectionColor, styles }: { blocks: Array<Extract<MarkdownBlock, { kind: "paragraph" | "heading" }>>; selectionColor: string; styles: ReturnType<typeof makeStyles> }) {
	return <Text selectable uiTextView selectionColor={selectionColor} style={styles.body}>
		{blocks.map((block, index) => <Text key={index} style={block.kind === "heading" ? [styles.heading, block.level > 2 && styles.smallHeading] : styles.body}>
			{index > 0 ? <Text style={styles.proseGap}>{"\n\n"}</Text> : null}{inline(block.text, styles)}
		</Text>)}
	</Text>;
}

/** One native text surface for selecting across every part of a response. */
export function SelectableResponseText({ text, source }: { text: string; source?: Omit<ChatExcerpt, "text"> }) {
	const t = useTheme();
	const addToChat = useContext(AddResponseToChat);
	const select = useCallback((selected: string) => source ? addToChat?.(selected, source) : addToChat?.(selected), [addToChat, source]);
	const onLayout = useAndroidResponseSelection(addToChat && source ? select : null);
	const styles = useThemedStyles(makeStyles);
	const selectionColor = textSelectionColors(t).selectionColor;
	return <Text selectable uiTextView selectionColor={selectionColor} style={styles.body} onLayout={onLayout} addToChatEnabled={Boolean(addToChat && source)} onAddToChat={(event) => select(event.nativeEvent.text)}>
		{parseBlocks(text).map((block, index) => {
			const separator = index > 0 ? "\n\n" : "";
			if (block.kind === "heading") return <Text key={index} style={[styles.heading, block.level > 2 && styles.smallHeading]}>{separator}{inline(block.text, styles)}</Text>;
			if (block.kind === "paragraph") return <Text key={index}>{separator}{inline(block.text, styles)}</Text>;
			if (block.kind === "quote") return <Text key={index} style={styles.quoteText}>{separator}{inline(block.text, styles)}</Text>;
			if (block.kind === "list") return <Text key={index}>{separator}{block.items.map((item, itemIndex) => <Text key={itemIndex} style={item.checked ? styles.taskDone : undefined}>{itemIndex > 0 ? "\n" : ""}{item.checked !== undefined ? (item.checked ? "☑ " : "☐ ") : block.ordered ? `${itemIndex + 1}. ` : "• "}{inline(item.text, styles)}</Text>)}</Text>;
			if (block.kind === "code") return <Text key={index} style={styles.selectionCode}>{separator}{block.language ? `${block.language}\n` : ""}{block.text}</Text>;
			if (block.kind === "table") return <Text key={index}>{separator}{[block.headers, ...block.rows].map((row, rowIndex) => <Text key={rowIndex}>{rowIndex > 0 ? "\n" : ""}{row.map((cell, cellIndex) => <Text key={cellIndex}>{cellIndex > 0 ? "  |  " : ""}{inline(cell, styles)}</Text>)}</Text>)}</Text>;
			if (block.kind === "image") return <Text key={index} style={styles.imageCaption}>{separator}{inline(block.alt || block.url, styles)}</Text>;
			return <Text key={index}>{separator}────────</Text>;
		})}
	</Text>;
}

function SelectableInlineText({ value, selectionColor, style, styles }: { value: string; selectionColor: string; style: StyleProp<TextStyle>; styles: ReturnType<typeof makeStyles> }) {
	const content = inline(value, styles);
	// The native UITextView flattens inline children into one accessibility node.
	// Keep links on RN Text on iOS so VoiceOver can focus their link role.
	const hasLink = content.some((node) => isValidElement(node) && node.type === MarkdownLink);
	return <Text selectable uiTextView={Platform.OS !== "ios" || !hasLink} selectionColor={selectionColor} style={style}>{content}</Text>;
}

let preferredCodeWrap = false;

function MarkdownImage({ alt, url, selectionColor, showCaption = true }: { alt: string; url: string; selectionColor: string; showCaption?: boolean }) {
	const styles = useThemedStyles(makeStyles);
	const [failed, setFailed] = useState(false);
	if (failed) return <Text selectable uiTextView selectionColor={selectionColor} style={styles.imageFallback}>Image unavailable: {alt || url}</Text>;
	return <View style={styles.imageCard}><Image accessibilityLabel={alt || "Conversation image"} accessibilityIgnoresInvertColors source={{ uri: url }} resizeMode="contain" onError={() => setFailed(true)} style={styles.image} />{alt && showCaption ? <Text selectable uiTextView selectionColor={selectionColor} style={styles.imageCaption}>{alt}</Text> : null}</View>;
}

function MarkdownTable({ headers, rows, selectionColor }: { headers: string[]; rows: string[][]; selectionColor: string }) {
	const styles = useThemedStyles(makeStyles);
	return <ScrollView horizontal showsHorizontalScrollIndicator><View style={styles.table}><View style={[styles.tableRow, styles.tableHeader]}>{headers.map((cell, index) => <Text key={index} selectable uiTextView selectionColor={selectionColor} style={[styles.tableCell, styles.tableHeaderText]}>{cell}</Text>)}</View>{rows.map((row, rowIndex) => <View key={rowIndex} style={styles.tableRow}>{headers.map((_, cellIndex) => <Text key={cellIndex} selectable uiTextView selectionColor={selectionColor} style={styles.tableCell}>{row[cellIndex] ?? ""}</Text>)}</View>)}</View></ScrollView>;
}

/** How long Copy shows its checkmark before it turns back. */
const COPIED_MS = 1_400;

function CodeBlock({ text, language, streaming }: { text: string; language?: string; streaming?: boolean }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const feedback = usePressFeedback("icon");
	const [copied, setCopied] = useState(false);
	const [wrap, setWrap] = useState(preferredCodeWrap);
	useEffect(() => {
		if (!copied) return;
		const timer = setTimeout(() => setCopied(false), COPIED_MS);
		return () => clearTimeout(timer);
	}, [copied]);
	return (
		<View style={styles.codeCard}>
			<View style={styles.codeHeader}>
				<Text style={styles.codeLanguage}>{language || "code"}</Text>
				{/* A toggle: while on it sits on a filled backing, so its state never
				    rests on colour alone, and it is announced as selected. */}
				<Pressable
					accessibilityRole="button"
					accessibilityLabel="Wrap code"
					accessibilityState={{ selected: wrap }}
					onPress={() => { preferredCodeWrap = !wrap; setWrap(!wrap); }}
					android_ripple={feedback.android_ripple}
					style={({ pressed }) => [styles.codeAction, pressed && feedback.pressed]}
				>
					<View style={[styles.codeToggle, wrap && styles.codeToggleOn]}>
						<AppSymbol name="wrapLines" size={iconSize.md} color={wrap ? t.textPrimary : t.textTertiary} />
					</View>
				</Pressable>
				<Pressable
					accessibilityRole="button"
					accessibilityLabel="Copy code"
					onPress={() => {
						void Clipboard.setStringAsync(text);
						setCopied(true);
						haptics.success();
					}}
					android_ripple={feedback.android_ripple}
					style={({ pressed }) => [styles.codeAction, pressed && feedback.pressed]}
				>
					<AppSymbol name={copied ? "checkmark" : "copy"} size={iconSize.md} color={copied ? t.green : t.textTertiary} />
				</Pressable>
			</View>
			<ScrollView horizontal={!wrap} showsHorizontalScrollIndicator={!wrap} contentContainerStyle={wrap ? styles.codeWrap : undefined}>
				<HighlightedCodeText code={text} language={language} streaming={streaming} style={[styles.codeText, wrap && styles.codeTextWrap]} />
			</ScrollView>
		</View>
	);
}

function MarkdownLink({ url, label, style }: { url: string; label: string; style: StyleProp<TextStyle> }) {
	const open = useContext(OpenChatLink) ?? defaultOpenChatLink;
	return <Text accessibilityRole="link" style={style} onPress={() => open(url)}>{label}</Text>;
}

function inline(text: string, styles: ReturnType<typeof makeStyles>, linksPressable = true): ReactNode[] {
	const pattern = /(\[([^\]]+)\]\((https?:\/\/[^\s)]+)\)|<(https?:\/\/[^\s>]+)>|`([^`]+)`|\*\*([^*]+)\*\*|__([^_]+)__|~~([^~]+)~~|\*([^*\n]+)\*|_([^_\n]+)_|(https?:\/\/[^\s<]+))/g;
	const nodes: ReactNode[] = [];
	let at = 0;
	let match: RegExpExecArray | null;
	while ((match = pattern.exec(text))) {
		if (match.index > at) nodes.push(text.slice(at, match.index));
		if ((match[7] || match[10]) && !allowsUnderscoreEmphasis(text, match.index, pattern.lastIndex)) {
			nodes.push(match[0]);
		} else if ((match[2] && match[3]) || match[4] || match[11]) {
			const url = match[3] ?? match[4] ?? match[11];
			const label = match[2] ?? url;
			nodes.push(linksPressable ? <MarkdownLink key={`${match.index}-link`} url={url} label={label} style={styles.link} /> : <Text key={`${match.index}-link`} style={styles.link}>{label}</Text>);
		} else if (match[5]) {
			nodes.push(<Text key={`${match.index}-code`} style={styles.inlineCode}>{match[5]}</Text>);
		} else if (match[6] || match[7]) {
			nodes.push(<Text key={`${match.index}-strong`} style={styles.strong}>{match[6] ?? match[7]}</Text>);
		} else if (match[8]) {
			nodes.push(<Text key={`${match.index}-strike`} style={styles.strike}>{match[8]}</Text>);
		} else {
			nodes.push(<Text key={`${match.index}-em`} style={styles.em}>{match[9] ?? match[10]}</Text>);
		}
		at = match.index + match[0].length;
	}
	if (at < text.length) nodes.push(text.slice(at));
	return nodes;
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		root: { gap: 10 },
		body: { fontFamily: "Geist_400Regular", color: t.textPrimary, fontSize: prose.fontSize, lineHeight: prose.lineHeight },
		proseGap: { fontSize: 10, lineHeight: 10 },
		heading: { fontFamily: "Geist_600SemiBold", color: t.textPrimary, fontSize: type.title3.fontSize, lineHeight: type.title3.lineHeight, fontWeight: "600", marginTop: space.xs },
		smallHeading: { fontFamily: "Geist_400Regular", fontSize: type.callout.fontSize, lineHeight: type.callout.lineHeight },
		strong: { fontFamily: "Geist_600SemiBold", fontWeight: "600" },
		em: { fontStyle: "italic" },
		strike: { textDecorationLine: "line-through" },
		link: { color: t.highlight, textDecorationLine: "underline" },
		inlineCode: { color: t.accent, fontFamily: t.fontMono, fontSize: type.subheadline.fontSize, backgroundColor: t.bgSubtle },
		selectionCode: { color: t.textPrimary, fontFamily: t.fontMono, fontSize: type.footnote.fontSize, lineHeight: 20, backgroundColor: t.bgSubtle },
		list: { gap: space.xxs },
		listRow: { flexDirection: "row", alignItems: "flex-start", gap: space.sm, paddingRight: space.xxs },
		// Text defaults to no shrinking inside a row on Android, so numbered list
		// items with inline code or links can render past the right edge.
		listBody: { flex: 1, minWidth: 0 },
		// The marker sits on the same line as prose, so it borrows prose leading.
		marker: { fontFamily: "Geist_400Regular", width: 20, flexShrink: 0, color: t.textTertiary, fontSize: type.subheadline.fontSize, lineHeight: prose.lineHeight, textAlign: "right" },
		// A drawn marker's column takes the numbered marker's width.
		markerBox: { width: 20 },
		bullet: { width: 5, height: 5, borderRadius: radius.pill, backgroundColor: t.textTertiary },
		taskDone: { color: t.textTertiary, textDecorationLine: "line-through" },
		quote: { borderLeftWidth: 2, borderLeftColor: t.borderStrong, paddingLeft: space.md },
		quoteText: { fontFamily: "Geist_400Regular", color: t.textSecondary, fontSize: type.subheadline.fontSize, lineHeight: prose.lineHeight, fontStyle: "italic" },
		rule: { height: rowDividerWidth, backgroundColor: t.borderSubtle, marginVertical: space.xxs },
		codeCard: { borderRadius: radius.md, borderCurve: "continuous", overflow: "hidden", borderWidth: rowDividerWidth, borderColor: t.borderSubtle, backgroundColor: t.bgColumn },
		codeHeader: { paddingLeft: space.md, flexDirection: "row", alignItems: "center", borderBottomWidth: rowDividerWidth, borderBottomColor: t.borderSubtle },
		codeLanguage: { flex: 1, color: t.textTertiary, fontFamily: t.fontMono, fontSize: type.footnote.fontSize },
		codeAction: { width: touchTarget, height: touchTarget, alignItems: "center", justifyContent: "center" },
		codeToggle: { padding: space.xxs, borderRadius: radius.sm, borderCurve: "continuous" },
		codeToggleOn: { backgroundColor: t.accentTint },
		// Code keeps looser leading than UI text at the same size.
		codeText: { color: t.textPrimary, fontFamily: t.fontMono, fontSize: type.footnote.fontSize, lineHeight: 20, padding: space.md },
		codeWrap: { flexGrow: 1 },
		codeTextWrap: { flexShrink: 1 },
		imageCard: { overflow: "hidden", borderRadius: radius.md, borderCurve: "continuous", borderWidth: rowDividerWidth, borderColor: t.borderSubtle, backgroundColor: t.bgColumn },
		image: { width: "100%", minHeight: 190, maxHeight: 420 },
		imageCaption: { fontFamily: "Geist_400Regular", color: t.textTertiary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, paddingHorizontal: space.sm, paddingVertical: space.xs, borderTopWidth: rowDividerWidth, borderTopColor: t.borderSubtle },
		imageFallback: { fontFamily: "Geist_400Regular", color: t.textTertiary, fontSize: type.footnote.fontSize, fontStyle: "italic" },
		table: { borderWidth: rowDividerWidth, borderColor: t.borderSubtle, borderRadius: radius.md, borderCurve: "continuous", overflow: "hidden" },
		tableRow: { flexDirection: "row", borderTopWidth: rowDividerWidth, borderTopColor: t.borderSubtle },
		tableHeader: { borderTopWidth: 0, backgroundColor: t.bgSubtle },
		tableCell: { fontFamily: "Geist_400Regular", minWidth: 110, maxWidth: 260, color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, paddingHorizontal: space.sm, paddingVertical: space.xs, borderLeftWidth: rowDividerWidth, borderLeftColor: t.borderSubtle },
		tableHeaderText: { fontFamily: "Geist_600SemiBold", color: t.textPrimary, fontWeight: "600" },
	});
