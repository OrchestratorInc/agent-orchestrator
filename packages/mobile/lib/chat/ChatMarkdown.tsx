import { UITextView as Text } from "@bsky.app/react-native-uitextview";
import * as Clipboard from "expo-clipboard";
import { createContext, Fragment, isValidElement, lazy, memo, Suspense, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { Image, Platform, Pressable, ScrollView, StyleSheet, View, type StyleProp, type TextStyle } from "react-native";
import { AppSymbol } from "../AppSymbol";
import { rowDividerWidth } from "../divider";
import { haptics } from "../haptics";
import { openGitHub } from "../openGitHub";
import { usePressFeedback } from "../pressFeedback";
import type { Theme } from "../theme";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { textSelectionColors } from "../textSelection";
import { imageSourceForPlatform } from "../imageSource";
import { useAndroidResponseSelection } from "./useAndroidResponseSelection";
import { FirstLineMarker } from "./FirstLineMarker";
import { HighlightedCodeText } from "./HighlightedCodeText";
import { isMermaidFence } from "./mermaidFence";
import { allowsUnderscoreEmphasis } from "./inlineEmphasis";
import { parseBlocks, repairStreamingInline, type MarkdownBlock, type MarkdownListItem, type TableAlign } from "./markdownBlocks";
import { iconSize, prose, radius, space, touchTarget, type } from "../tokens";
import type { ChatExcerpt } from "./types";
import { sessionLink } from "./automationMessages";
import { chatFileReference, isBareFileReference } from "./fileLinks";
import { useFilesNavigation } from "../files/navigation";

// The diagram renderer (a WebView and the engine's loader) loads on the first
// Mermaid fence, so text without one never pulls it in.
const LazyMermaidBlock = lazy(() => import("./MermaidBlock").then((module) => ({ default: module.MermaidBlock })));

/** A Mermaid fence: its diagram once the renderer loads, the code block until then. */
function DiagramFence({ code, streaming, codeBlock }: { code: string; streaming?: boolean; codeBlock: ReactNode }) {
	return <Suspense fallback={codeBlock}><LazyMermaidBlock code={code} streaming={streaming} codeBlock={codeBlock} /></Suspense>;
}

// The conversation screen decides how a tapped link opens (it knows the AO host
// that the agent's localhost links map onto); the renderer only reports the tap.

const AddResponseToChat = createContext<((text: string, source?: Omit<ChatExcerpt, "text">) => void) | null>(null);
const OpenChatLink = createContext<((url: string) => void) | null>(null);
// The name to show for a session a bare `ao://sessions/…` link names, and a way
// to ask for one the surface does not know yet.
type SessionNames = { name?: (sessionId: string) => string | undefined; want?: (sessionId: string) => void };
const SessionNameOf = createContext<SessionNames | null>(null);

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

/**
 * How links in this chat open. `onLinkOpen` also receives `ao://sessions/…`
 * links: only the surface knows which machine the conversation is on, so a
 * session link is a link only under a provider, and `sessionName` lets a bare
 * one read as the session's name rather than its URL.
 */
export function ChatLinkProvider({ onLinkOpen, onAddToChat, sessionName, onSessionNameNeeded, children }: { onLinkOpen: (url: string) => void; onAddToChat?: (text: string, source?: Omit<ChatExcerpt, "text">) => void; sessionName?: (sessionId: string) => string | undefined; onSessionNameNeeded?: (sessionId: string) => void; children: ReactNode }) {
	const names = useMemo<SessionNames>(() => ({ name: sessionName, want: onSessionNameNeeded }), [onSessionNameNeeded, sessionName]);
	return <OpenChatLink.Provider value={onLinkOpen}><AddResponseToChat.Provider value={onAddToChat ?? null}><SessionNameOf.Provider value={names}>{children}</SessionNameOf.Provider></AddResponseToChat.Provider></OpenChatLink.Provider>;
}

/**
 * Small native CommonMark renderer for the conversation surface.
 *
 * It deliberately covers the structures coding agents emit constantly (code,
 * headings, lists, quotes, links and inline emphasis). Unknown Markdown remains
 * readable text; the renderer never hides content because a provider used
 * syntax it does not know.
 */
export const ChatMarkdown = memo(function ChatMarkdown({ text, streaming = false, continuousSelection = false, selectionSource, onAddSelection, resolveImage, imagesInPlace = false, nestedScroll = false, diagrams = true }: {
	text: string;
	streaming?: boolean;
	continuousSelection?: boolean;
	selectionSource?: Omit<ChatExcerpt, "text">;
	/**
	 * Offers the text-selection menu's "Add to chat" outside a conversation (an
	 * artifact), handing it the selected text. Needs `continuousSelection`.
	 */
	onAddSelection?: (text: string) => void;
	/**
	 * Turns an image reference into what to load: a relative path in a
	 * document resolves against its folder, with the connection's headers.
	 * Undefined means the image cannot be loaded.
	 */
	resolveImage?: (url: string) => MarkdownImageSource | undefined;
	/** Draws each image where the document places it, rather than after the text. */
	imagesInPlace?: boolean;
	/**
	 * Android: tables and unwrapped code keep sideways drags when a parent also
	 * scrolls sideways (the artifact pager). Off elsewhere: a nested-scroll view
	 * under the finger stops a parent from taking the drag even where it takes
	 * no touches, as in a filmstrip preview.
	 */
	nestedScroll?: boolean;
	/**
	 * Draws ```mermaid fences as diagrams, as the desktop does. Off for a
	 * passive preview (a gallery card), where a fence stays code.
	 */
	diagrams?: boolean;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const selectionColor = textSelectionColors(t).selectionColor;
	// While a reply streams, an unfinished `**bold` reads as bold rather than as asterisks.
	const shown = streaming ? repairStreamingInline(text) : text;
	const blocks = parseBlocks(shown, { relativeImages: Boolean(resolveImage) });
	const openFiles = Boolean(useFilesNavigation());
	const selectCode = useResponseSelection(selectionSource, onAddSelection);
	if (continuousSelection) {
		// Native selection cannot cross sibling views. Keep each run of response text
		// in one text surface, broken only where a block needs a surface of its own:
		// a table's grid, a code card (with its copy, wrap and sideways scroll, as
		// on the desktop), a quote's bar and a rule. Image previews follow without
		// repeating captions.
		const images = imagesInPlace ? [] : blocks.filter((block): block is Extract<MarkdownBlock, { kind: "image" }> => block.kind === "image");
		return <View style={styles.root}>
			{splitRuns(blocks, imagesInPlace).map((run, index) => {
				if (Array.isArray(run)) return <SelectableResponseText key={index} blocks={run} source={selectionSource} onSelect={onAddSelection} />;
				if (run.kind === "code") {
					const codeBlock = <CodeBlock key={index} {...run} streaming={streaming} nestedScroll={nestedScroll} onAddSelection={selectCode} />;
					return diagrams && !streaming && isMermaidFence(run.language) ? <DiagramFence key={index} code={run.text} codeBlock={codeBlock} /> : codeBlock;
				}
				if (run.kind === "quote") return <View key={index} style={styles.quote}><SelectableResponseText blocks={[run]} source={selectionSource} onSelect={onAddSelection} /></View>;
				if (run.kind === "rule") return <View key={index} style={styles.rule} />;
				if (run.kind === "image") return <MarkdownImage key={index} alt={run.alt} url={run.url} selectionColor={selectionColor} resolve={resolveImage} />;
				return <MarkdownTable key={index} headers={run.headers} align={run.align} rows={run.rows} selectionColor={selectionColor} nestedScroll={nestedScroll} />;
			})}
			{images.map((image, index) => <MarkdownImage key={`${image.url}-${index}`} alt={image.alt} url={image.url} selectionColor={selectionColor} showCaption={false} resolve={resolveImage} />)}
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
			if (Platform.OS === "ios" && hasInlineLink(block.text, styles, openFiles)) {
				flushProse();
				content.push(<SelectableInlineText key={content.length} value={block.text} selectionColor={selectionColor} style={block.kind === "heading" ? headingStyle(block.level, styles) : styles.body} styles={styles} />);
			} else prose.push(block);
			continue;
		}
		flushProse();
		const index = content.length;
		if (block.kind === "code" && diagrams && isMermaidFence(block.language)) content.push(<DiagramFence key={index} code={block.text} streaming={streaming} codeBlock={<CodeBlock {...block} streaming={streaming} nestedScroll={nestedScroll} />} />);
		else if (block.kind === "code") content.push(<CodeBlock key={index} {...block} streaming={streaming} nestedScroll={nestedScroll} />);
		else if (block.kind === "image") content.push(<MarkdownImage key={index} alt={block.alt} url={block.url} selectionColor={selectionColor} resolve={resolveImage} />);
		else if (block.kind === "table") content.push(<MarkdownTable key={index} headers={block.headers} align={block.align} rows={block.rows} selectionColor={selectionColor} nestedScroll={nestedScroll} />);
		else if (block.kind === "rule") content.push(<View key={index} style={styles.rule} />);
		else if (block.kind === "quote") content.push(<View key={index} style={styles.quote}><SelectableInlineText value={block.text} selectionColor={selectionColor} style={styles.quoteText} styles={styles} /></View>);
		else content.push(<View key={index} style={styles.list}>{block.items.map((item, itemIndex) => <View key={itemIndex} style={[styles.listRow, item.depth > 0 && { paddingLeft: item.depth * LIST_INDENT }]}><ListMarker checked={item.checked} number={item.number} depth={item.depth} styles={styles} /><SelectableInlineText value={item.text} selectionColor={selectionColor} style={[styles.body, styles.listBody, item.checked && styles.taskDone]} styles={styles} /></View>)}</View>);
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
function ListMarker({ checked, number, depth, styles }: { checked?: boolean; number?: number; depth: number; styles: ReturnType<typeof makeStyles> }) {
	const t = useTheme();
	if (checked !== undefined) return <FirstLineMarker lineStyle={styles.body} style={styles.markerBox}><AppSymbol name={checked ? TASK_DONE : TASK_OPEN} size={iconSize.md} color={t.textTertiary} accessibilityLabel={checked ? "Done" : "Not done"} /></FirstLineMarker>;
	if (number !== undefined) return <Text style={styles.marker}>{`${number}.`}</Text>;
	// A nested level reads by its marker as well as its indent: a ring, then a square.
	return <FirstLineMarker lineStyle={styles.body} style={styles.markerBox}><View style={[styles.bullet, depth === 1 && styles.bulletRing, depth > 1 && styles.bulletSquare]} /></FirstLineMarker>;
}

/** How far each nesting level of a list steps in: one marker column and its gap. */
const LIST_INDENT = 20 + space.sm;

/** The marker a list item shows inside one run of selectable text. */
function listMarkerText(item: MarkdownListItem): string {
	if (item.checked !== undefined) return item.checked ? "☑ " : "☐ ";
	if (item.number !== undefined) return `${item.number}. `;
	return item.depth === 0 ? "• " : item.depth === 1 ? "◦ " : "▪ ";
}

/**
 * Headings step down in size but stay in the conversation's voice, as on the
 * desktop: an agent's "## Findings" is a paragraph label, not a page title.
 */
function headingStyle(level: number, styles: ReturnType<typeof makeStyles>): StyleProp<TextStyle> {
	return level <= 1 ? styles.heading : level <= 3 ? [styles.heading, styles.subheading] : [styles.heading, styles.minorHeading];
}

type TableBlock = Extract<MarkdownBlock, { kind: "table" }>;
type ImageBlock = Extract<MarkdownBlock, { kind: "image" }>;
type CodeFence = Extract<MarkdownBlock, { kind: "code" }>;
type QuoteBlock = Extract<MarkdownBlock, { kind: "quote" }>;
type RuleBlock = Extract<MarkdownBlock, { kind: "rule" }>;
/** A block drawn on a surface of its own, between runs of selectable text. */
type StandaloneBlock = TableBlock | ImageBlock | CodeFence | QuoteBlock | RuleBlock;
type TextRunBlock = Exclude<MarkdownBlock, TableBlock>;

/** What a markdown image loads: its URL, with headers when it needs the connection's credential. */
export type MarkdownImageSource = { uri: string; headers?: Record<string, string> };

/**
 * Runs of text one selection can span, broken by each block that needs a
 * surface of its own: a table (a grid cannot live inside text), a code fence
 * (its card, or its diagram), a quote (its bar), a rule, and, for a document,
 * each image so it sits in place.
 */
export function splitRuns(blocks: MarkdownBlock[], splitImages: boolean): Array<Array<TextRunBlock> | StandaloneBlock> {
	const runs: Array<Array<TextRunBlock> | StandaloneBlock> = [];
	for (const block of blocks) {
		const last = runs[runs.length - 1];
		if (block.kind === "table" || block.kind === "code" || block.kind === "quote" || block.kind === "rule" || (splitImages && block.kind === "image")) runs.push(block);
		else if (Array.isArray(last)) last.push(block);
		else runs.push([block]);
	}
	return runs;
}

function hasInlineLink(value: string, styles: ReturnType<typeof makeStyles>, openFiles: boolean): boolean {
	return containsLink(inline(value, styles, true, undefined, openFiles));
}

/** Whether any of these nodes is a link, including one nested in emphasis. */
function containsLink(nodes: ReactNode[]): boolean {
	return nodes.some((node) => isValidElement<{ children?: ReactNode }>(node)
		&& (node.type === MarkdownLink || (Array.isArray(node.props.children) && containsLink(node.props.children))));
}

function SelectableProseGroup({ blocks, selectionColor, styles }: { blocks: Array<Extract<MarkdownBlock, { kind: "paragraph" | "heading" }>>; selectionColor: string; styles: ReturnType<typeof makeStyles> }) {
	const openFiles = Boolean(useFilesNavigation());
	return <Text selectable uiTextView selectionColor={selectionColor} style={styles.body}>
		{blocks.map((block, index) => <Text key={index} style={block.kind === "heading" ? headingStyle(block.level, styles) : styles.body}>
			{index > 0 ? <Text style={block.kind === "heading" ? styles.headingGap : styles.proseGap}>{"\n\n"}</Text> : null}{inline(block.text, styles, true, undefined, openFiles)}
		</Text>)}
	</Text>;
}

/**
 * What the selection menu's "Add to chat" does with response text: hands it to
 * the surface's own handler, or quotes it into the conversation's composer. In a
 * conversation, only a finished message (one with a source) can be quoted.
 */
function useResponseSelection(source: Omit<ChatExcerpt, "text"> | undefined, onSelect: ((text: string) => void) | undefined): ((text: string) => void) | undefined {
	const addToChat = useContext(AddResponseToChat);
	const enabled = Boolean(onSelect || (addToChat && source));
	const select = useCallback((selected: string) => onSelect ? onSelect(selected) : source ? addToChat?.(selected, source) : addToChat?.(selected), [addToChat, onSelect, source]);
	return enabled ? select : undefined;
}

/** One native text surface for selecting across a run of response blocks. */
export function SelectableResponseText({ blocks, source, onSelect }: { blocks: TextRunBlock[]; source?: Omit<ChatExcerpt, "text">; onSelect?: (text: string) => void }) {
	const t = useTheme();
	const select = useResponseSelection(source, onSelect);
	const onLayout = useAndroidResponseSelection(select ?? null);
	const styles = useThemedStyles(makeStyles);
	const openFiles = Boolean(useFilesNavigation());
	const selectionColor = textSelectionColors(t).selectionColor;
	const text = (value: string) => inline(value, styles, true, undefined, openFiles);
	// The space between blocks is a short blank line, not a full line of prose,
	// so paragraphs sit as close as they do on the desktop; a heading takes more.
	const gap = (block: TextRunBlock, index: number) => index > 0 ? <Text style={block.kind === "heading" ? styles.headingGap : styles.proseGap}>{"\n\n"}</Text> : null;
	const quoted = blocks.length === 1 && blocks[0].kind === "quote";
	return <Text selectable uiTextView selectionColor={selectionColor} style={quoted ? styles.quoteText : styles.body} onLayout={onLayout} addToChatEnabled={Boolean(select)} onAddToChat={(event) => select?.(event.nativeEvent.text)}>
		{blocks.map((block, index) => {
			if (block.kind === "heading") return <Fragment key={index}>{gap(block, index)}<Text style={headingStyle(block.level, styles)}>{text(block.text)}</Text></Fragment>;
			if (block.kind === "paragraph") return <Fragment key={index}>{gap(block, index)}<Text>{text(block.text)}</Text></Fragment>;
			if (block.kind === "quote") return <Fragment key={index}>{gap(block, index)}<Text style={styles.quoteText}>{text(block.text)}</Text></Fragment>;
			if (block.kind === "list") return <Fragment key={index}>{gap(block, index)}{block.items.map((item, itemIndex) => <Text key={itemIndex} style={item.checked ? styles.taskDone : undefined}>
				{itemIndex > 0 ? "\n" : ""}{"\u2003".repeat(item.depth)}<Text style={styles.inlineMarker}>{listMarkerText(item)}</Text>{text(item.text)}
			</Text>)}</Fragment>;
			if (block.kind === "code") return <Fragment key={index}>{gap(block, index)}<Text style={styles.selectionCode}>{block.language ? `${block.language}\n` : ""}{block.text}</Text></Fragment>;
			if (block.kind === "image") return <Fragment key={index}>{gap(block, index)}<Text style={styles.imageCaption}>{text(block.alt || block.url)}</Text></Fragment>;
			return <Fragment key={index}>{gap(block, index)}<Text>────────</Text></Fragment>;
		})}
	</Text>;
}

function SelectableInlineText({ value, selectionColor, style, styles, codeStyle }: { value: string; selectionColor: string; style: StyleProp<TextStyle>; styles: ReturnType<typeof makeStyles>; codeStyle?: StyleProp<TextStyle> }) {
	const openFiles = Boolean(useFilesNavigation());
	const content = inline(value, styles, true, codeStyle, openFiles);
	// The native UITextView flattens inline children into one accessibility node.
	// Keep links on RN Text on iOS so VoiceOver can focus their link role.
	const hasLink = containsLink(content);
	return <Text selectable uiTextView={Platform.OS !== "ios" || !hasLink} selectionColor={selectionColor} style={style}>{content}</Text>;
}

let preferredCodeWrap = false;

function MarkdownImage({ alt, url, selectionColor, showCaption = true, resolve }: { alt: string; url: string; selectionColor: string; showCaption?: boolean; resolve?: (url: string) => MarkdownImageSource | undefined }) {
	const styles = useThemedStyles(makeStyles);
	const [failed, setFailed] = useState(false);
	const source = resolve ? resolve(url) : { uri: url };
	if (failed || !source) return <Text selectable uiTextView selectionColor={selectionColor} style={styles.imageFallback}>Image unavailable: {alt || url}</Text>;
	return <View style={styles.imageCard}><Image accessibilityLabel={alt || "Conversation image"} accessibilityIgnoresInvertColors source={source.headers ? imageSourceForPlatform(source) : source} resizeMode="contain" onError={() => setFailed(true)} style={styles.image} />{alt && showCaption ? <Text selectable uiTextView selectionColor={selectionColor} style={styles.imageCaption}>{alt}</Text> : null}</View>;
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

function MarkdownTable({ headers, align, rows, selectionColor, nestedScroll }: { headers: string[]; align: TableAlign[]; rows: string[][]; selectionColor: string; nestedScroll: boolean }) {
	const styles = useThemedStyles(makeStyles);
	const widths = headers.map((header, column) => tableColumnWidth([header, ...rows.map((row) => row[column] ?? "")]));
	const row = (cells: string[], header: boolean) => headers.map((_, column) => <View key={column} style={[styles.tableCell, column === 0 && styles.tableFirstCell, { width: widths[column], flexGrow: widths[column] }]}>
		<SelectableInlineText value={cells[column] ?? ""} selectionColor={selectionColor} style={[header ? styles.tableHeaderText : styles.tableText, align[column] && { textAlign: align[column] }]} styles={styles} codeStyle={styles.tableCode} />
	</View>);
	return <ScrollView horizontal nestedScrollEnabled={nestedScroll} showsHorizontalScrollIndicator contentContainerStyle={styles.tableScroll}>
		<View style={styles.table}>
			<View style={[styles.tableRow, styles.tableHeader]}>{row(headers, true)}</View>
			{rows.map((cells, index) => <View key={index} style={styles.tableRow}>{row(cells, false)}</View>)}
		</View>
	</ScrollView>;
}

/** How long Copy shows its checkmark before it turns back. */
const COPIED_MS = 1_400;

function CodeBlock({ text, language, streaming, nestedScroll = false, onAddSelection }: { text: string; language?: string; streaming?: boolean; nestedScroll?: boolean; onAddSelection?: (text: string) => void }) {
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
			<ScrollView horizontal={!wrap} nestedScrollEnabled={nestedScroll && !wrap} showsHorizontalScrollIndicator={!wrap} contentContainerStyle={wrap ? styles.codeWrap : undefined}>
				<HighlightedCodeText code={text} language={language} streaming={streaming} style={[styles.codeText, wrap && styles.codeTextWrap]} onAddSelection={onAddSelection} />
			</ScrollView>
		</View>
	);
}

function MarkdownLink({ url, label, style }: { url: string; label: string; style: StyleProp<TextStyle> }) {
	const provided = useContext(OpenChatLink);
	const files = useFilesNavigation();
	const names = useContext(SessionNameOf);
	const session = sessionLink(url);
	// A bare link reads as the session it names, asked for once when the surface does not know it; an authored label is kept.
	const bareSessionId = session && label === url ? session.sessionId : undefined;
	const known = bareSessionId ? names?.name?.(bareSessionId) : undefined;
	useEffect(() => {
		if (bareSessionId && !known) names?.want?.(bareSessionId);
	}, [bareSessionId, known, names]);
	const file = session || WEB_OR_AO_LINK.test(url) ? undefined : chatFileReference(url);
	const bare = Boolean(file && isBareFileReference(file.path));
	const bareTarget = bare && file ? files?.bareFile?.(file.path) : undefined;
	const bareName = bare && file ? file.path : "";
	useEffect(() => {
		if (bareName && bareTarget === undefined) files?.wantBareFile?.(bareName);
	}, [bareName, bareTarget, files]);
	if (session) {
		const text = bareSessionId ? known || bareSessionId : label;
		// Without a provider nothing knows which machine the session is on, so it stays words.
		if (!provided) return <Text>{text}</Text>;
		return <Text accessibilityRole="link" accessibilityHint="Opens this session's chat" style={style} onPress={() => provided(url)}>{text}</Text>;
	}
	if (file) {
		// Only a session's surface can open its files, and a bare name only when
		// the session has that file; otherwise the label stays words.
		const target = bare ? bareTarget : file.path;
		if (!files || !target) return <Text>{label}</Text>;
		return <Text accessibilityRole="link" accessibilityHint={`Opens ${target}`} style={style} onPress={() => { haptics.tap(); files.openFile(target, undefined, file.line); }}>{label}</Text>;
	}
	const open = provided ?? defaultOpenChatLink;
	return <Text accessibilityRole="link" style={style} onPress={() => open(url)}>{label}</Text>;
}

const WEB_OR_AO_LINK = /^(?:https?|ao):\/\//i;

/**
 * `openFiles`: whether this surface can open a session's files. Where it cannot,
 * a file link is its label as plain words, not a link that does nothing, so a
 * paragraph of them stays one native text (iOS draws a link-holding block apart).
 */
function inline(text: string, styles: ReturnType<typeof makeStyles>, linksPressable = true, codeStyle?: StyleProp<TextStyle>, openFiles = true): ReactNode[] {
	// Web links, AO's own `ao://` links, and file links (`[a.ts:12](src/a.ts:12)`); a bare session link stops before prose punctuation.
	// A destination may hold balanced parentheses, as a route group does: `app/(tabs)/index.tsx`.
	// Bold runs may hold a single `*` or `_` (italic, say) but not a closing pair;
	// a whole `*italic*` inside one may end on the bold's own closing run: `**bold *italic***`.
	const pattern = /\\(?<escaped>[!-/:-@[-`{-~])|\[(?<label>[^\]]+)\]\((?<href>(?:[^\s()]|\([^\s()]*\))+)\)|<(?<angle>(?:https?|ao):\/\/[^\s>]+)>|`(?<code>[^`]+)`|\*\*\*(?<strongEm>[^*\n]+)\*\*\*|\*\*(?<strong>(?:\*[^*\n]+\*|[^*]|\*(?!\*))+)\*\*|__(?<underStrong>(?:[^_]|_(?!_))+)__|~~(?<strike>[^~]+)~~|\*(?<em>[^*\n]+)\*|_(?<underEm>[^_\n]+)_|(?<bare>https?:\/\/[^\s<]+)|(?<session>ao:\/\/sessions\/[^\s<>()]*[^\s<>().,;:!?'"])/g;
	const nodes: ReactNode[] = [];
	let at = 0;
	let match: RegExpExecArray | null;
	while ((match = pattern.exec(text))) {
		const g = match.groups!;
		if (match.index > at) nodes.push(text.slice(at, match.index));
		let end = match.index + match[0].length;
		if (g.escaped !== undefined) {
			// `\*` is a literal asterisk, not the start of emphasis.
			nodes.push(g.escaped);
		} else if ((g.underStrong || g.underEm) && !allowsUnderscoreEmphasis(text, match.index, pattern.lastIndex)) {
			nodes.push(match[0]);
		} else if (g.href && !WEB_OR_AO_LINK.test(g.href) && !chatFileReference(g.href)) {
			// Neither a page nor a file (mailto:, an anchor): keep the Markdown as
			// written, and keep reading inside it so its code and emphasis render.
			nodes.push("[");
			at = match.index + 1;
			pattern.lastIndex = at;
			continue;
		} else if (g.href && !openFiles && !WEB_OR_AO_LINK.test(g.href)) {
			nodes.push(g.label);
		} else if (g.href || g.angle || g.bare || g.session) {
			// A bare address ends before the sentence's punctuation, as GitHub reads it.
			const url = g.href ?? g.angle ?? g.session ?? trimBareUrl(g.bare!);
			if (g.bare) end = match.index + url.length;
			const label = g.label ?? url;
			nodes.push(linksPressable ? <MarkdownLink key={`${match.index}-link`} url={url} label={label} style={styles.link} /> : <Text key={`${match.index}-link`} style={styles.link}>{label}</Text>);
		} else if (g.code) {
			nodes.push(<Text key={`${match.index}-code`} style={codeStyle ? [styles.inlineCode, codeStyle] : styles.inlineCode}>{g.code}</Text>);
		} else if (g.strongEm) {
			nodes.push(<Text key={`${match.index}-strong-em`} style={[styles.strong, styles.em]}>{inline(g.strongEm, styles, linksPressable, codeStyle, openFiles)}</Text>);
		} else if (g.strong || g.underStrong) {
			// Emphasis holds inline Markdown of its own: a link inside bold is still a link.
			nodes.push(<Text key={`${match.index}-strong`} style={styles.strong}>{inline(g.strong ?? g.underStrong, styles, linksPressable, codeStyle, openFiles)}</Text>);
		} else if (g.strike) {
			nodes.push(<Text key={`${match.index}-strike`} style={styles.strike}>{inline(g.strike, styles, linksPressable, codeStyle, openFiles)}</Text>);
		} else {
			nodes.push(<Text key={`${match.index}-em`} style={styles.em}>{inline(g.em ?? g.underEm, styles, linksPressable, codeStyle, openFiles)}</Text>);
		}
		at = end;
		pattern.lastIndex = end;
	}
	if (at < text.length) nodes.push(text.slice(at));
	return nodes;
}

/**
 * A bare web address without the punctuation that ends its sentence: a trailing
 * full stop, comma or quote, and a closing parenthesis it did not open.
 */
export function trimBareUrl(url: string): string {
	let end = url.length;
	for (;;) {
		const last = url[end - 1];
		if (/[.,;:!?'"*_~]/.test(last)) end--;
		else if (last === ")" && count(url.slice(0, end), "(") < count(url.slice(0, end), ")")) end--;
		else break;
	}
	return url.slice(0, end);
}

function count(text: string, char: string): number {
	return text.split(char).length - 1;
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		root: { gap: 10 },
		body: { fontFamily: "DMSans_400Regular", color: t.textPrimary, fontSize: prose.fontSize, lineHeight: prose.lineHeight },
		proseGap: { fontSize: 10, lineHeight: 10 },
		// A heading opens a section, so it sits further from the text above it.
		headingGap: { fontSize: 16, lineHeight: 16 },
		heading: { fontFamily: "DMSans_600SemiBold", color: t.textPrimary, fontSize: type.title3.fontSize, lineHeight: type.title3.lineHeight, fontWeight: "600", marginTop: space.xs },
		// `##` and `###` label a paragraph: prose size, semibold.
		subheading: { fontSize: prose.fontSize, lineHeight: prose.lineHeight },
		minorHeading: { color: t.textSecondary, fontSize: type.subheadline.fontSize, lineHeight: type.subheadline.lineHeight },
		strong: { fontFamily: "DMSans_600SemiBold", fontWeight: "600" },
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
		marker: { fontFamily: "DMSans_400Regular", width: 20, flexShrink: 0, color: t.textTertiary, fontSize: type.subheadline.fontSize, lineHeight: prose.lineHeight, textAlign: "right" },
		// A drawn marker's column takes the numbered marker's width.
		markerBox: { width: 20 },
		bullet: { width: 5, height: 5, borderRadius: radius.pill, backgroundColor: t.textTertiary },
		bulletRing: { backgroundColor: "transparent", borderWidth: 1, borderColor: t.textTertiary },
		bulletSquare: { borderRadius: 1 },
		// A marker inside selectable text steps back like a drawn one.
		inlineMarker: { color: t.textTertiary },
		taskDone: { color: t.textTertiary, textDecorationLine: "line-through" },
		quote: { borderLeftWidth: 2, borderLeftColor: t.borderStrong, paddingLeft: space.md },
		quoteText: { fontFamily: "DMSans_400Regular", color: t.textSecondary, fontSize: prose.fontSize, lineHeight: prose.lineHeight },
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
		imageCaption: { fontFamily: "DMSans_400Regular", color: t.textTertiary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, paddingHorizontal: space.sm, paddingVertical: space.xs, borderTopWidth: rowDividerWidth, borderTopColor: t.borderSubtle },
		imageFallback: { fontFamily: "DMSans_400Regular", color: t.textTertiary, fontSize: type.footnote.fontSize, fontStyle: "italic" },
		tableScroll: { flexGrow: 1 },
		table: { flexGrow: 1, borderWidth: rowDividerWidth, borderColor: t.borderSubtle, borderRadius: radius.md, borderCurve: "continuous", overflow: "hidden" },
		tableRow: { flexDirection: "row", borderTopWidth: rowDividerWidth, borderTopColor: t.borderSubtle },
		tableHeader: { borderTopWidth: 0, backgroundColor: t.bgSubtle },
		tableCell: { flexShrink: 0, paddingHorizontal: space.sm, paddingVertical: space.xs, borderLeftWidth: rowDividerWidth, borderLeftColor: t.borderSubtle },
		tableFirstCell: { borderLeftWidth: 0 },
		tableText: { fontFamily: "DMSans_400Regular", color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
		tableHeaderText: { fontFamily: "DMSans_600SemiBold", color: t.textPrimary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, fontWeight: "600" },
		tableCode: { fontSize: type.caption1.fontSize },
	});
