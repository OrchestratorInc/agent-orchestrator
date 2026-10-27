import { Feather } from "../icons";
import { UITextView as Text } from "@bsky.app/react-native-uitextview";
import * as Clipboard from "expo-clipboard";
import { createContext, isValidElement, memo, useCallback, useContext, useState, type ReactNode } from "react";
import { Image, Platform, Pressable, ScrollView, StyleSheet, View, type StyleProp, type TextStyle } from "react-native";
import { haptics } from "../haptics";
import { openGitHub } from "../openGitHub";
import type { Theme } from "../theme";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { textSelectionColors } from "../textSelection";
import { useAndroidResponseSelection } from "./useAndroidResponseSelection";
import { HighlightedCodeText } from "./HighlightedCodeText";
import { allowsUnderscoreEmphasis } from "./inlineEmphasis";
import { parseBlocks, type MarkdownBlock, type TableAlign } from "./markdownBlocks";
import { iconSize, microLabel, prose, radius, space, type } from "../tokens";
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
		// Native selection cannot cross sibling views. Keep each run of response text
		// in one text surface; only a table breaks the run, because a grid cannot
		// live inside text. Image previews follow without repeating captions.
		const images = blocks.filter((block): block is Extract<MarkdownBlock, { kind: "image" }> => block.kind === "image");
		return <View style={styles.root}>
			{splitAtTables(blocks).map((run, index) => Array.isArray(run)
				? <SelectableResponseText key={index} blocks={run} source={selectionSource} />
				: <MarkdownTable key={index} headers={run.headers} align={run.align} rows={run.rows} selectionColor={selectionColor} />)}
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
		else if (block.kind === "table") content.push(<MarkdownTable key={index} headers={block.headers} align={block.align} rows={block.rows} selectionColor={selectionColor} />);
		else if (block.kind === "rule") content.push(<View key={index} style={styles.rule} />);
		else if (block.kind === "quote") content.push(<View key={index} style={styles.quote}><SelectableInlineText value={block.text} selectionColor={selectionColor} style={styles.quoteText} styles={styles} /></View>);
		else content.push(<View key={index} style={styles.list}>{block.items.map((item, itemIndex) => <View key={itemIndex} style={styles.listRow}><Text style={styles.marker}>{item.checked !== undefined ? (item.checked ? "☑" : "☐") : block.ordered ? `${itemIndex + 1}.` : "•"}</Text><SelectableInlineText value={item.text} selectionColor={selectionColor} style={[styles.body, styles.listBody, item.checked && styles.taskDone]} styles={styles} /></View>)}</View>);
	}
	flushProse();
	return (
		<View style={styles.root}>{content}</View>
	);
});

type TableBlock = Extract<MarkdownBlock, { kind: "table" }>;

function splitAtTables(blocks: MarkdownBlock[]): Array<Array<Exclude<MarkdownBlock, TableBlock>> | TableBlock> {
	const runs: Array<Array<Exclude<MarkdownBlock, TableBlock>> | TableBlock> = [];
	for (const block of blocks) {
		const last = runs[runs.length - 1];
		if (block.kind === "table") runs.push(block);
		else if (Array.isArray(last)) last.push(block);
		else runs.push([block]);
	}
	return runs;
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

/** One native text surface for selecting across a run of response blocks. */
export function SelectableResponseText({ blocks, source }: { blocks: Array<Exclude<MarkdownBlock, TableBlock>>; source?: Omit<ChatExcerpt, "text"> }) {
	const t = useTheme();
	const addToChat = useContext(AddResponseToChat);
	const select = useCallback((selected: string) => source ? addToChat?.(selected, source) : addToChat?.(selected), [addToChat, source]);
	const onLayout = useAndroidResponseSelection(addToChat && source ? select : null);
	const styles = useThemedStyles(makeStyles);
	const selectionColor = textSelectionColors(t).selectionColor;
	return <Text selectable uiTextView selectionColor={selectionColor} style={styles.body} onLayout={onLayout} addToChatEnabled={Boolean(addToChat && source)} onAddToChat={(event) => select(event.nativeEvent.text)}>
		{blocks.map((block, index) => {
			const separator = index > 0 ? "\n\n" : "";
			if (block.kind === "heading") return <Text key={index} style={[styles.heading, block.level > 2 && styles.smallHeading]}>{separator}{inline(block.text, styles)}</Text>;
			if (block.kind === "paragraph") return <Text key={index}>{separator}{inline(block.text, styles)}</Text>;
			if (block.kind === "quote") return <Text key={index} style={styles.quoteText}>{separator}{inline(block.text, styles)}</Text>;
			if (block.kind === "list") return <Text key={index}>{separator}{block.items.map((item, itemIndex) => <Text key={itemIndex} style={item.checked ? styles.taskDone : undefined}>{itemIndex > 0 ? "\n" : ""}{item.checked !== undefined ? (item.checked ? "☑ " : "☐ ") : block.ordered ? `${itemIndex + 1}. ` : "• "}{inline(item.text, styles)}</Text>)}</Text>;
			if (block.kind === "code") return <Text key={index} style={styles.selectionCode}>{separator}{block.language ? `${block.language}\n` : ""}{block.text}</Text>;
			if (block.kind === "image") return <Text key={index} style={styles.imageCaption}>{separator}{inline(block.alt || block.url, styles)}</Text>;
			return <Text key={index}>{separator}────────</Text>;
		})}
	</Text>;
}

function SelectableInlineText({ value, selectionColor, style, styles, codeStyle }: { value: string; selectionColor: string; style: StyleProp<TextStyle>; styles: ReturnType<typeof makeStyles>; codeStyle?: StyleProp<TextStyle> }) {
	const content = inline(value, styles, true, codeStyle);
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

// React Native has no grid layout, so a column's width is estimated from its
// longest cell and every row uses that width; that keeps columns aligned.
// Columns then grow in proportion to fill a wide screen, or scroll when they
// overflow a narrow one. It must be `width`, not `flexBasis`: inside a
// horizontal ScrollView the row's width is undefined, and Yoga then ignores a
// numeric basis and sizes each cell to its own content.
const TABLE_CHAR_WIDTH = 7.4;
const TABLE_COLUMN_MIN = 72;
const TABLE_COLUMN_MAX = 220;

function tableColumnWidth(cells: string[]): number {
	const chars = Math.max(0, ...cells.map((cell) => cell.replace(/\[([^\]]+)\]\([^)]+\)/g, "$1").replace(/[*`~]/g, "").length));
	return Math.min(TABLE_COLUMN_MAX, Math.max(TABLE_COLUMN_MIN, Math.ceil(chars * TABLE_CHAR_WIDTH) + space.sm * 2));
}

function MarkdownTable({ headers, align, rows, selectionColor }: { headers: string[]; align: TableAlign[]; rows: string[][]; selectionColor: string }) {
	const styles = useThemedStyles(makeStyles);
	const widths = headers.map((header, column) => tableColumnWidth([header, ...rows.map((row) => row[column] ?? "")]));
	const row = (cells: string[], header: boolean) => headers.map((_, column) => <View key={column} style={[styles.tableCell, column === 0 && styles.tableFirstCell, { width: widths[column], flexGrow: widths[column] }]}>
		<SelectableInlineText value={cells[column] ?? ""} selectionColor={selectionColor} style={[header ? styles.tableHeaderText : styles.tableText, align[column] && { textAlign: align[column] }]} styles={styles} codeStyle={styles.tableCode} />
	</View>);
	return <ScrollView horizontal showsHorizontalScrollIndicator contentContainerStyle={styles.tableScroll}>
		<View style={styles.table}>
			<View style={[styles.tableRow, styles.tableHeader]}>{row(headers, true)}</View>
			{rows.map((cells, index) => <View key={index} style={styles.tableRow}>{row(cells, false)}</View>)}
		</View>
	</ScrollView>;
}

function CodeBlock({ text, language, streaming }: { text: string; language?: string; streaming?: boolean }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const [copied, setCopied] = useState(false);
	const [wrap, setWrap] = useState(preferredCodeWrap);
	return (
		<View style={styles.codeCard}>
			<View style={styles.codeHeader}>
				<Text style={styles.codeLanguage}>{language || "code"}</Text>
				<Pressable accessibilityRole="button" accessibilityLabel={wrap ? "Disable code wrapping" : "Wrap code"} accessibilityState={{ selected: wrap }} onPress={() => { haptics.select(); preferredCodeWrap = !wrap; setWrap(!wrap); }} style={styles.copyButton}><Feather name="corner-down-left" size={iconSize.xs} color={wrap ? t.accent : t.textTertiary} /><Text style={[styles.copyLabel, wrap && { color: t.accent }]}>Wrap</Text></Pressable>
				<Pressable
					accessibilityRole="button"
					accessibilityLabel="Copy code"
					onPress={() => {
						void Clipboard.setStringAsync(text);
						setCopied(true);
						haptics.success();
						setTimeout(() => setCopied(false), 1_400);
					}}
					style={styles.copyButton}
				>
					<Feather name={copied ? "check" : "copy"} size={iconSize.xs} color={copied ? t.green : t.textTertiary} />
					<Text style={[styles.copyLabel, copied && { color: t.green }]}>{copied ? "Copied" : "Copy"}</Text>
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
	return <Text accessibilityRole="link" style={style} onPress={() => { haptics.tap(); open(url); }}>{label}</Text>;
}

function inline(text: string, styles: ReturnType<typeof makeStyles>, linksPressable = true, codeStyle?: StyleProp<TextStyle>): ReactNode[] {
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
			nodes.push(<Text key={`${match.index}-code`} style={codeStyle ? [styles.inlineCode, codeStyle] : styles.inlineCode}>{match[5]}</Text>);
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
		taskDone: { color: t.textTertiary, textDecorationLine: "line-through" },
		quote: { borderLeftWidth: 2, borderLeftColor: t.borderStrong, paddingLeft: 12 },
		quoteText: { fontFamily: "Geist_400Regular", color: t.textSecondary, fontSize: type.subheadline.fontSize, lineHeight: prose.lineHeight, fontStyle: "italic" },
		rule: { height: 1, backgroundColor: t.borderSubtle, marginVertical: space.xxs },
		codeCard: { borderRadius: radius.md, borderCurve: "continuous", overflow: "hidden", borderWidth: 1, borderColor: t.borderSubtle, backgroundColor: t.bgColumn },
		codeHeader: { minHeight: 34, paddingHorizontal: space.md, flexDirection: "row", alignItems: "center", borderBottomWidth: 1, borderBottomColor: t.borderSubtle },
		codeLanguage: { flex: 1, color: t.textTertiary, ...microLabel, fontFamily: t.fontMono, textTransform: "uppercase" },
		copyButton: { flexDirection: "row", alignItems: "center", gap: space.xxs, paddingVertical: space.xs, paddingLeft: space.md },
		copyLabel: { fontFamily: "Geist_600SemiBold", color: t.textTertiary, fontSize: type.caption2.fontSize, fontWeight: "600" },
		// Code keeps looser leading than UI text at the same size.
		codeText: { color: t.textPrimary, fontFamily: t.fontMono, fontSize: type.footnote.fontSize, lineHeight: 20, padding: space.md },
		codeWrap: { flexGrow: 1 },
		codeTextWrap: { flexShrink: 1 },
		imageCard: { overflow: "hidden", borderRadius: radius.md, borderCurve: "continuous", borderWidth: 1, borderColor: t.borderSubtle, backgroundColor: t.bgColumn },
		image: { width: "100%", minHeight: 190, maxHeight: 420 },
		imageCaption: { fontFamily: "Geist_400Regular", color: t.textTertiary, fontSize: type.caption2.fontSize, paddingHorizontal: space.sm, paddingVertical: space.xs, borderTopWidth: 1, borderTopColor: t.borderSubtle },
		imageFallback: { fontFamily: "Geist_400Regular", color: t.textTertiary, fontSize: type.caption1.fontSize, fontStyle: "italic" },
		tableScroll: { flexGrow: 1 },
		table: { flexGrow: 1, borderWidth: 1, borderColor: t.borderSubtle, borderRadius: radius.sm, borderCurve: "continuous", overflow: "hidden" },
		tableRow: { flexDirection: "row", borderTopWidth: StyleSheet.hairlineWidth, borderTopColor: t.borderSubtle },
		tableHeader: { borderTopWidth: 0, backgroundColor: t.bgSubtle },
		tableCell: { flexShrink: 0, paddingHorizontal: space.sm, paddingVertical: space.xs, borderLeftWidth: StyleSheet.hairlineWidth, borderLeftColor: t.borderSubtle },
		tableFirstCell: { borderLeftWidth: 0 },
		tableText: { fontFamily: "Geist_400Regular", color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
		tableHeaderText: { fontFamily: "Geist_600SemiBold", color: t.textPrimary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, fontWeight: "600" },
		tableCode: { fontSize: type.caption1.fontSize },
	});
