import { LexicalComposer } from "@lexical/react/LexicalComposer";
import { ContentEditable } from "@lexical/react/LexicalContentEditable";
import { LexicalErrorBoundary } from "@lexical/react/LexicalErrorBoundary";
import { HistoryPlugin } from "@lexical/react/LexicalHistoryPlugin";
import { PlainTextPlugin } from "@lexical/react/LexicalPlainTextPlugin";
import { useLexicalComposerContext } from "@lexical/react/LexicalComposerContext";
import {
	$addUpdateTag,
	$createRangeSelectionFromDom,
	$getNodeByKey,
	$getRoot,
	$getSelection,
	$isElementNode,
	$isRangeSelection,
	$isTextNode,
	$setSelection,
	$createParagraphNode,
	$createTextNode,
	CLEAR_HISTORY_COMMAND,
	COMMAND_PRIORITY_HIGH,
	COMMAND_PRIORITY_LOW,
	DecoratorNode,
	getDOMSelectionFromTarget,
	HISTORY_MERGE_TAG,
	HISTORY_PUSH_TAG,
	KEY_BACKSPACE_COMMAND,
	KEY_ENTER_COMMAND,
	KEY_ESCAPE_COMMAND,
	KEY_TAB_COMMAND,
	TextNode,
	type EditorConfig,
	type LexicalEditor,
	type LexicalNode,
	type NodeKey,
	type RangeSelection,
	type SerializedLexicalNode,
	type SerializedTextNode,
	type Spread,
} from "lexical";
import {
	createContext,
	forwardRef,
	useCallback,
	useContext,
	useEffect,
	useImperativeHandle,
	useId,
	useLayoutEffect,
	useRef,
	useState,
	type ClipboardEvent,
	type JSX,
	type KeyboardEvent,
} from "react";
import { Box, Image as ImageIcon } from "@/components/app-icons";
import { useTranslation } from "react-i18next";
import { cn } from "../../lib/utils";
import { createDraftFaviconWarmer } from "../../lib/link-preload";
import { composerFileIcon } from "./composerFileIcon";
import { findActiveTrigger, type TriggerKind } from "./composerSuggest";
import { listBackspace, listEnter, listIndent, type LinePrefixEdit } from "./composerLists";
import { splitInlineImagePaths } from "./messageAttachments";
import type { ComposerSentToken } from "./sentComposerTokens";

export type ComposerTrigger = {
	kind: TriggerKind;
	key: string;
	nodeKey: string;
	start: number;
	end: number;
	query: string;
};

export type ComposerEditorSnapshot = {
	text: string;
	hasText: boolean;
	trigger?: ComposerTrigger;
	/** The caret's offset into text; undefined without a selection in the editor. */
	caret?: number;
};

export type ComposerPromptSuggestion = { id: string; text: string; onDismiss?: () => void };

export type ComposerEditorHandle = {
	focus(): void;
	clear(): void;
	/** Replaces the text with the caret at its end, or at its start when asked. */
	setText(text: string, caret?: "start" | "end"): void;
	/** Where a collapsed caret sits relative to the whole text; undefined for a range. */
	caretBoundary(): { atStart: boolean; atEnd: boolean } | undefined;
	insertToken(trigger: ComposerTrigger, value: string): void;
	/**
	 * Shows a dictation transcript at the caret (or the end), replacing the one
	 * shown before it, so the words appear as they are recognized.
	 */
	dictate(text: string): void;
	/** Ends the dictated run: kept as ordinary text, or removed. */
	endDictation(keep: boolean): void;
	/** Inserts a file reference chip at the caret (or the end) that sends `wire`. */
	insertReference(path: string, display: string, wire: string): void;
	/**
	 * Hold the caret's place for images still being staged. Returns a reservation
	 * for fillImages, so the chips land where the user pasted, not wherever the
	 * caret has moved by the time staging finishes.
	 */
	reserveImages(): string | undefined;
	/** Replace a reservation with one chip per staged image path (none removes it). */
	fillImages(reservation: string, paths: string[]): void;
	/** Drop every inline chip for an image that left the attachment list. */
	removeImage(path: string): void;
	/**
	 * Drop chips whose image is not in `attached` (an undo can restore a chip
	 * after its image was removed). Typed text is never touched.
	 */
	pruneImages(attached: string[]): void;
	getSnapshot(): ComposerEditorSnapshot;
	/** The skill and file chips in the text, so the sent message can draw them again. */
	getTokens(): ComposerSentToken[];
};

/** What an inline image chip shows for a staged path; the path itself is the wire text. */
export type ComposerImage = { path: string; name: string; src?: string };

const ComposerImages = createContext<ComposerImage[]>([]);

/** An empty path is a reservation whose image is still being staged. */
function ComposerImageChip({ path }: { path: string }) {
	const { t } = useTranslation();
	const images = useContext(ComposerImages);
	const index = images.findIndex((candidate) => candidate.path === path);
	const image = images[index];
	if (!path) {
		return (
			<span
				data-composer-token="image-pending"
				contentEditable={false}
				className="mx-0.5 inline-flex items-center gap-1 rounded-md border border-dashed border-border-strong px-1.5 py-0.5 align-middle text-[0.9em] leading-none text-muted-foreground select-none"
			>
				<ImageIcon aria-hidden="true" className="size-3 shrink-0" />
				…
			</span>
		);
	}
	return (
		<span
			data-composer-token="image"
			data-value={path}
			contentEditable={false}
			title={image?.name}
			className="mx-0.5 inline-flex max-w-48 items-center gap-1 rounded-md border border-border-strong bg-interactive-hover py-0.5 pl-0.5 pr-1.5 align-middle text-[0.9em] leading-none text-foreground select-none"
		>
			{image?.src ? (
				<img src={image.src} alt="" className="size-4 shrink-0 rounded-sm object-cover" />
			) : (
				<ImageIcon aria-hidden="true" className="size-3 shrink-0" />
			)}
			{/* Numbered like the sent message will be; the file name stays in the tooltip. */}
			<span className="truncate">
				{image ? t("chat.image.numbered", { index: index + 1 }) : t("chat.image.untitled")}
			</span>
		</span>
	);
}

type TokenKind = "skill" | "file" | "image";

const completionHandledEvents = new WeakSet<Event>();
const PROGRAMMATIC_TEXT_UPDATE_TAG = "ao:composer-programmatic-text";

type SerializedComposerTokenNode = Spread<
	{
		kind: TokenKind;
		value: string;
		display: string;
		wire: string;
	},
	SerializedLexicalNode
>;

/** How long a dictated letter takes to fade in (the `stream-char` animation in styles.css). */
const DICTATION_FADE_MS = 300;
/** Letters that arrive together fade in this far apart, left to right. */
const DICTATION_LETTER_STAGGER_MS = 14;

/** The draft's text sits this far inside the composer's padding on both sides. */
const COMPOSER_TEXT_INSET = "px-[7px]";
/**
 * Reserved on both edges of whichever element scrolls the draft, and on the placeholder
 * standing in for it. A classic scrollbar then neither reflows the text when it appears
 * nor pushes the text off-centre; overlay scrollbars reserve nothing.
 */
const COMPOSER_SCROLL_GUTTER = "chat-composer-scrollbar [scrollbar-gutter:stable_both-edges]";

/**
 * One letter of a dictation in progress. A letter fades in once, when it first
 * appears (the streamed-prose `stream-char` fade), after a delay that staggers
 * the letters of a word. Revisions change the letter in place, and copies
 * Lexical makes later (clone, split) never fade, so nothing flashes. The run
 * becomes plain text once the last fade has finished.
 */
class DictationLetterNode extends TextNode {
	__fadeDelay: number | undefined;

	static getType(): string {
		return "dictation-letter";
	}

	static clone(node: DictationLetterNode): DictationLetterNode {
		return new DictationLetterNode(node.__text, node.__key);
	}

	static importJSON(serialized: SerializedTextNode): DictationLetterNode {
		return new DictationLetterNode(serialized.text).updateFromJSON(serialized);
	}

	constructor(text: string, key?: NodeKey, fadeDelay?: number) {
		super(text, key);
		this.__fadeDelay = fadeDelay;
	}

	createDOM(config: EditorConfig, editor?: LexicalEditor): HTMLElement {
		const dom = super.createDOM(config, editor);
		if (this.__fadeDelay !== undefined) {
			dom.classList.add("stream-char");
			// Hidden until its turn, then fades in like the rest.
			dom.style.animationDelay = `${this.__fadeDelay}ms`;
			dom.style.animationFillMode = "backwards";
		}
		return dom;
	}
}

class ComposerTokenNode extends DecoratorNode<JSX.Element> {
	__kind: TokenKind;
	__value: string;
	__display: string;
	__wire: string;

	static getType(): string {
		return "composer-token";
	}

	static clone(node: ComposerTokenNode): ComposerTokenNode {
		return new ComposerTokenNode(
			node.__kind,
			node.__value,
			node.__display,
			node.__wire,
			node.__key,
		);
	}

	static importJSON(serialized: SerializedComposerTokenNode): ComposerTokenNode {
		return new ComposerTokenNode(
			serialized.kind,
			serialized.value,
			serialized.display,
			serialized.wire,
		);
	}

	constructor(kind: TokenKind, value: string, display: string, wire: string, key?: NodeKey) {
		super(key);
		this.__kind = kind;
		this.__value = value;
		this.__display = display;
		this.__wire = wire;
	}

	exportJSON(): SerializedComposerTokenNode {
		return {
			...super.exportJSON(),
			type: "composer-token",
			version: 1,
			kind: this.__kind,
			value: this.__value,
			display: this.__display,
			wire: this.__wire,
		};
	}

	createDOM(_config: EditorConfig): HTMLElement {
		return document.createElement("span");
	}

	updateDOM(): false {
		return false;
	}

	isInline(): true {
		return true;
	}

	isKeyboardSelectable(): false {
		return false;
	}

	getTextContent(): string {
		return this.__wire;
	}

	decorate(): JSX.Element {
		if (this.__kind === "image") return <ComposerImageChip path={this.__value} />;
		const Icon = this.__kind === "skill" ? Box : composerFileIcon(this.__value);
		return (
			<span
				data-composer-token={this.__kind}
				data-value={this.__value}
				contentEditable={false}
				className={cn(
					"mx-0.5 inline-flex items-center gap-1 rounded-md border border-border-strong bg-interactive-hover px-1.5 py-0.5 align-middle text-[0.9em] leading-none select-none",
					this.__kind === "skill"
						? "text-logo-accent"
						: "text-foreground",
				)}
			>
				<Icon aria-hidden="true" className="size-3 shrink-0" />
				{this.__display}
			</span>
		);
	}
}

function $createComposerTokenNode(kind: TokenKind, value: string): ComposerTokenNode {
	if (kind === "image") return new ComposerTokenNode(kind, value, value, value);
	const wire = kind === "skill" ? `/${value}` : /\s/.test(value) ? `"${value}"` : value;
	const slash = value.lastIndexOf("/");
	const display = kind === "skill" ? wire : slash >= 0 ? value.slice(slash + 1) : value;
	return new ComposerTokenNode(kind, value, display, wire);
}

function $insertComposerReference(path: string, display: string, wire: string): void {
	let selection = $getSelection();
	if (!$isRangeSelection(selection)) {
		$getRoot().selectEnd();
		selection = $getSelection();
	}
	if (!$isRangeSelection(selection)) return;
	selection.insertNodes([new ComposerTokenNode("file", path, display, wire), $createTextNode(" ")]);
}

function $serializeComposer(): string {
	return $getRoot()
		.getChildren()
		.map((child) => child.getTextContent())
		.join("\n");
}

function $composerTokens(): ComposerSentToken[] {
	return $getRoot()
		.getChildren()
		.flatMap((paragraph) => ($isElementNode(paragraph) ? paragraph.getChildren() : []))
		.flatMap<ComposerSentToken>((child) =>
			child instanceof ComposerTokenNode && child.__kind !== "image"
				? [{ kind: child.__kind, value: child.__value, display: child.__display, wire: child.__wire }]
				: [],
		);
}

function $insertComposerToken(trigger: ComposerTrigger, value: string): boolean {
	const node = $getNodeByKey<LexicalNode>(trigger.nodeKey);
	if (!$isTextNode(node)) return false;
	const text = node.getTextContent();
	const expected = `${trigger.kind === "skill" ? "/" : "@"}${trigger.query}`;
	if (text.slice(trigger.start, trigger.end) !== expected) return false;

	const before = text.slice(0, trigger.start);
	const after = text.slice(trigger.end);
	const token = $createComposerTokenNode(trigger.kind, value);
	const tail = $createTextNode(/^\s/.test(after) ? after : ` ${after}`);
	if (before) {
		const head = $createTextNode(before);
		node.replace(head);
		head.insertAfter(token);
	} else {
		node.replace(token);
	}
	token.insertAfter(tail);
	tail.select(1, 1);
	return true;
}

function $replaceEditorText(text: string, attached: string[] = [], caret: "start" | "end" = "end"): void {
	const root = $getRoot();
	root.clear();
	for (const line of text.split("\n")) {
		const paragraph = $createParagraphNode();
		// A restored draft is plain text; paths of still-attached images become chips
		// again. Any other path stays the text the user typed.
		for (const segment of splitInlineImagePaths(line, (path) => attached.includes(path))) {
			paragraph.append(
				segment.path === undefined
					? $createTextNode(segment.text)
					: $createComposerTokenNode("image", segment.path),
			);
		}
		root.append(paragraph);
	}
	if (caret === "start") root.selectStart();
	else root.selectEnd();
}

/** True when nothing precedes (or follows) the point in the whole document. */
function $isAtDocumentEdge(point: RangeSelection["anchor"], edge: "start" | "end"): boolean {
	let node: LexicalNode = point.getNode();
	const size = $isTextNode(node) ? node.getTextContentSize() : $isElementNode(node) ? node.getChildrenSize() : 0;
	if (point.offset !== (edge === "start" ? 0 : size)) return false;
	for (let parent = node.getParent(); parent; node = parent, parent = node.getParent()) {
		if ((edge === "start" ? node.getPreviousSibling() : node.getNextSibling()) !== null) return false;
	}
	return true;
}

/**
 * Read from the DOM: Lexical adopts a native caret move on `selectionchange`, which
 * can still be queued when the next arrow key arrives.
 */
function $caretBoundary(editor: LexicalEditor): { atStart: boolean; atEnd: boolean } | undefined {
	const selection = $createRangeSelectionFromDom(getDOMSelectionFromTarget(editor.getRootElement()), editor) ?? $getSelection();
	if (!$isRangeSelection(selection) || !selection.isCollapsed()) return undefined;
	return {
		atStart: $isAtDocumentEdge(selection.anchor, "start"),
		atEnd: $isAtDocumentEdge(selection.anchor, "end"),
	};
}

/** True when the character before the caret would run into an inserted chip's text. */
function $joinsPreviousWord(selection: RangeSelection): boolean {
	const { anchor } = selection;
	const node = anchor.getNode();
	const before = $isTextNode(node)
		? node.getTextContent().slice(0, anchor.offset) || (node.getPreviousSibling()?.getTextContent() ?? "")
		: $isElementNode(node) && anchor.offset > 0
			? (node.getChildAtIndex(anchor.offset - 1)?.getTextContent() ?? "")
			: "";
	return /\S$/.test(before);
}

function $reserveImages(): string | undefined {
	const reservation = $createComposerTokenNode("image", "");
	const nodes: LexicalNode[] = [reservation, $createTextNode(" ")];
	let selection = $getSelection();
	if (!$isRangeSelection(selection)) {
		// A drop or the file picker can leave no selection; the end is the natural place.
		$getRoot().selectEnd();
		selection = $getSelection();
	}
	if (!$isRangeSelection(selection)) return undefined;
	if ($joinsPreviousWord(selection)) nodes.unshift($createTextNode(" "));
	selection.insertNodes(nodes);
	return reservation.getKey();
}

function $fillImages(reservation: string, paths: string[]): void {
	const node = $getNodeByKey(reservation);
	// Deleted while staging: the user removed it on purpose, so add nothing back.
	if (!(node instanceof ComposerTokenNode)) return;
	if (paths.length === 0) {
		$removeChip(node);
		return;
	}
	let last: LexicalNode = node;
	paths.forEach((path, index) => {
		const chip = $createComposerTokenNode("image", path);
		if (index === 0) {
			node.replace(chip);
		} else {
			const space = $createTextNode(" ");
			last.insertAfter(space);
			space.insertAfter(chip);
		}
		last = chip;
	});
}

/** Remove a chip and one following space so the words around it don't double-space. */
function $removeChip(chip: LexicalNode): void {
	const next = chip.getNextSibling();
	if ($isTextNode(next) && next.getTextContent().startsWith(" ")) {
		if (next.getTextContent() === " ") next.remove();
		else next.setTextContent(next.getTextContent().slice(1));
	}
	chip.remove();
}

function $removeImageTokens(remove: (path: string) => boolean): void {
	for (const paragraph of $getRoot().getChildren()) {
		if (!$isElementNode(paragraph)) continue;
		for (const child of paragraph.getChildren()) {
			if (child instanceof ComposerTokenNode && child.__kind === "image" && remove(child.__value)) $removeChip(child);
		}
	}
}

// The text content of container's descendants before target, in document
// order, counted as $serializeComposer counts it.
function $textBefore(container: LexicalNode, target: LexicalNode): number {
	if (!$isElementNode(container)) return 0;
	let count = 0;
	for (const child of container.getChildren()) {
		if (child.is(target)) return count;
		if ($isElementNode(child) && child.isParentOf(target)) return count + $textBefore(child, target);
		count += child.getTextContent().length;
	}
	return count;
}

/** Where the selection's focus sits in $serializeComposer's text. */
function $caretOffset(): number | undefined {
	const selection = $getSelection();
	if (!$isRangeSelection(selection)) return undefined;
	const point = selection.focus;
	const node = point.getNode();
	let offset = 0;
	for (const paragraph of $getRoot().getChildren()) {
		if (paragraph.is(node) || ($isElementNode(paragraph) && paragraph.isParentOf(node))) {
			const within = point.type === "element" && $isElementNode(node)
				? node.getChildren().slice(0, point.offset).reduce((sum, child) => sum + child.getTextContent().length, 0)
				: point.offset;
			return offset + $textBefore(paragraph, node) + within;
		}
		offset += paragraph.getTextContent().length + 1;
	}
	return undefined;
}

/**
 * The caret's line as $serializeComposer counts it, with the lines above it.
 * Only for a collapsed caret in the plain text node the line starts with, since
 * list keys rewrite the line's start; anything else keeps the default key.
 */
function $caretLine() {
	const selection = $getSelection();
	if (!$isRangeSelection(selection) || !selection.isCollapsed() || selection.anchor.type !== "text") return undefined;
	const node = selection.anchor.getNode();
	if (!$isTextNode(node) || node.getType() !== "text") return undefined;
	const caret = $caretOffset();
	if (caret === undefined) return undefined;
	const text = $serializeComposer();
	const start = caret === 0 ? 0 : text.lastIndexOf("\n", caret - 1) + 1;
	const column = caret - start;
	if (selection.anchor.offset !== column) return undefined;
	const end = text.indexOf("\n", caret);
	return {
		selection,
		node,
		column,
		line: text.slice(start, end < 0 ? undefined : end),
		previous: start === 0 ? [] : text.slice(0, start - 1).split("\n"),
	};
}

/** Rewrites the start of the caret's line as one undo step, keeping the caret on the same text. */
function $applyLinePrefixEdit(node: TextNode, column: number, edit: LinePrefixEdit): boolean {
	if (node.getTextContentSize() < edit.remove) return false;
	$addUpdateTag(HISTORY_PUSH_TAG);
	const selection = node.select(0, edit.remove);
	if (edit.insert) selection.insertText(edit.insert);
	else selection.removeText();
	const caret = Math.max(column - edit.remove, 0) + edit.insert.length;
	if (caret === edit.insert.length) return true;
	const after = $getSelection();
	const anchor = $isRangeSelection(after) ? after.anchor.getNode() : undefined;
	if ($isTextNode(anchor)) anchor.select(caret, caret);
	return true;
}

function editorSnapshot(): ComposerEditorSnapshot {
	const text = $serializeComposer();
	const caret = $caretOffset();
	const selection = $getSelection();
	if (!$isRangeSelection(selection) || !selection.isCollapsed()) {
		return { text, hasText: text.trim().length > 0, caret };
	}

	const anchor = selection.anchor;
	const node = anchor.getNode();
	if (!$isTextNode(node) || anchor.type !== "text") {
		return { text, hasText: text.trim().length > 0, caret };
	}

	const active = findActiveTrigger(node.getTextContent(), anchor.offset);
	if (!active) return { text, hasText: text.trim().length > 0, caret };
	return {
		text,
		hasText: text.trim().length > 0,
		caret,
		trigger: {
			...active,
			key: `${node.getKey()}:${active.start}`,
			nodeKey: node.getKey(),
			end: anchor.offset,
		},
	};
}

function focusEditor(editor: LexicalEditor): void {
	const root = editor.getRootElement();
	const native = getDOMSelectionFromTarget(root);
	// A native caret move can precede Lexical's queued selectionchange. Adopt it
	// before focus restores the editor selection, including background clicks.
	if (root && native?.anchorNode && native.focusNode && root.contains(native.anchorNode) && root.contains(native.focusNode)) {
		editor.update(() => {
			const selection = $createRangeSelectionFromDom(native, editor);
			if (selection) $setSelection(selection);
		}, { discrete: true });
	}
	editor.focus();
	if (root && document.activeElement !== root) root.focus({ preventScroll: true });
}

const EditorBridge = forwardRef<
	ComposerEditorHandle,
	{
		disabled?: boolean;
		enterSends: boolean;
		promptSuggestion?: ComposerPromptSuggestion;
		onDismissSuggestion: () => void;
		onEmptyChange: (empty: boolean) => void;
		/** Staged paths of the images attached right now. */
		attachedImages: () => string[];
		onChange: (snapshot: ComposerEditorSnapshot) => void;
		onComplete: (snapshot: ComposerEditorSnapshot, key: "Enter" | "Tab") => string | undefined;
		onEnter: (snapshot: ComposerEditorSnapshot, event: globalThis.KeyboardEvent) => boolean;
	}
>(function EditorBridge({ disabled, enterSends, promptSuggestion, onDismissSuggestion, onEmptyChange, attachedImages, onChange, onComplete, onEnter }, ref) {
	const [editor] = useLexicalComposerContext();
	/** The dictated letters, after an empty anchor node, and when the last fade ends. */
	const dictation = useRef<{ anchor: NodeKey; keys: NodeKey[]; prefix: string; settled: number } | null>(null);

	useEffect(() => editor.setEditable(!disabled), [disabled, editor]);

	// Links typed or pasted here load their site icons before they are sent.
	useEffect(() => {
		const warmer = createDraftFaviconWarmer();
		warmer.update(editor.getEditorState().read(() => $getRoot().getTextContent()));
		const unregister = editor.registerTextContentListener((text) => warmer.update(text));
		return () => {
			unregister();
			warmer.dispose();
		};
	}, [editor]);

	useImperativeHandle(
		ref,
		() => ({
			focus: () => focusEditor(editor),
			clear: () => {
				editor.update(() => {
					$replaceEditorText("");
					editor.dispatchCommand(CLEAR_HISTORY_COMMAND, undefined);
				}, {
					discrete: true,
					tag: PROGRAMMATIC_TEXT_UPDATE_TAG,
				});
			},
			setText: (text, caret) => {
				editor.update(() => {
					$replaceEditorText(text, attachedImages(), caret);
					editor.dispatchCommand(CLEAR_HISTORY_COMMAND, undefined);
				}, {
					discrete: true,
					tag: PROGRAMMATIC_TEXT_UPDATE_TAG,
				});
			},
			insertToken: (trigger, value) => {
				editor.update(() => {
					$insertComposerToken(trigger, value);
				}, { discrete: true });
			},
			dictate: (text) => {
				const continuing = dictation.current !== null;
				editor.update(() => {
					let run = dictation.current;
					if (!run) {
						if (!text) return;
						let selection = $getSelection();
						if (!$isRangeSelection(selection)) {
							$getRoot().selectEnd();
							selection = $getSelection();
						}
						if (!$isRangeSelection(selection)) return;
						const { anchor } = selection;
						const before = anchor.type === "text" ? anchor.getNode().getTextContent().slice(0, anchor.offset) : "";
						// Holds the run's place; it stays empty.
						const anchorNode = new DictationLetterNode("");
						selection.insertNodes([anchorNode]);
						run = { anchor: anchorNode.getKey(), keys: [], prefix: before && !/\s$/.test(before) ? " " : "", settled: 0 };
						dictation.current = run;
					}
					const letters = Array.from(run.prefix + text);
					const nodes = run.keys.map((key) => $getNodeByKey(key)).filter($isTextNode);
					let last: TextNode | null = $getNodeByKey<TextNode>(run.anchor);
					if (!last) return;
					const keys: NodeKey[] = [];
					let arriving = 0;
					letters.forEach((letter, index) => {
						let node = nodes[index];
						if (node) {
							// A revised letter changes in place, so it does not flash.
							if (node.getTextContent() !== letter) node.setTextContent(letter);
						} else {
							const delay = arriving++ * DICTATION_LETTER_STAGGER_MS;
							node = new DictationLetterNode(letter, undefined, delay);
							last!.insertAfter(node);
							run!.settled = Math.max(run!.settled, performance.now() + delay + DICTATION_FADE_MS);
						}
						last = node;
						keys.push(node.getKey());
					});
					for (const extra of nodes.slice(letters.length)) extra.remove();
					run.keys = keys;
					last.selectEnd();
				}, continuing ? { discrete: true, tag: HISTORY_MERGE_TAG } : { discrete: true });
			},
			endDictation: (keep) => {
				const run = dictation.current;
				dictation.current = null;
				if (!run) return;
				const finish = () => editor.update(() => {
					const letters = run.keys.map((key) => $getNodeByKey(key)).filter($isTextNode);
					const anchor = $getNodeByKey(run.anchor);
					const text = letters.map((letter) => letter.getTextContent()).join("");
					if (keep && text && anchor) {
						const plain = $createTextNode(text);
						anchor.insertAfter(plain);
						plain.selectEnd();
					}
					for (const node of [anchor, ...letters]) node?.remove();
				}, { discrete: true, tag: HISTORY_MERGE_TAG });
				// Kept letters become plain text after their fades, so none is cut short.
				if (keep) window.setTimeout(finish, Math.max(run.settled - performance.now(), 0));
				else finish();
			},
			insertReference: (path, display, wire) => {
				editor.update(() => {
					$insertComposerReference(path, display, wire);
				}, { discrete: true });
			},
			reserveImages: () => {
				let reservation: string | undefined;
				editor.update(() => {
					reservation = $reserveImages();
				}, { discrete: true });
				return reservation;
			},
			fillImages: (reservation, paths) => {
				editor.update(() => $fillImages(reservation, paths), { discrete: true });
			},
			removeImage: (path) => {
				editor.update(() => $removeImageTokens((candidate) => candidate === path), { discrete: true });
			},
			pruneImages: (attached) => {
				editor.update(() => $removeImageTokens((candidate) => !attached.includes(candidate)), { discrete: true });
			},
			getSnapshot: () => editor.getEditorState().read(editorSnapshot),
			getTokens: () => editor.getEditorState().read($composerTokens),
			caretBoundary: () => editor.read(() => $caretBoundary(editor)),
		}),
		[attachedImages, editor],
	);

	useLayoutEffect(() => {
		// Draft edits and Tab acceptance only hide the hint; Escape dismisses it.
		const updateEmpty = () => onEmptyChange($serializeComposer().length === 0);
		editor.getEditorState().read(updateEmpty);
		return editor.registerUpdateListener(({ editorState, tags }) => {
			editorState.read(updateEmpty);
			if (tags.has(PROGRAMMATIC_TEXT_UPDATE_TAG)) return;
			editorState.read(() => onChange(editorSnapshot()));
		});
	}, [editor, onChange, onEmptyChange]);

	useEffect(() => {
		const complete = (event: globalThis.KeyboardEvent | null, key: "Enter" | "Tab") => {
			// The React capture handler owns send/menu keys before Lexical's native
			// bubble listener. A prevented event was already handled there and must not
			// also insert a newline or a second completion token.
			if (event?.defaultPrevented) return true;
			if (event?.isComposing || event?.keyCode === 229 || event?.shiftKey || editor.isComposing()) return false;
			if (key === "Enter" && !enterSends && !event?.metaKey && !event?.ctrlKey) return false;
			const snapshot = editorSnapshot();
			if (
				key === "Tab" && promptSuggestion && !snapshot.text &&
				!event?.altKey && !event?.ctrlKey && !event?.metaKey
			) {
				const selection = $getSelection();
				if (!$isRangeSelection(selection) || !selection.isCollapsed()) return false;
				$addUpdateTag(HISTORY_PUSH_TAG);
				// End a generated @path or /skill with a boundary, including after draft
				// restore. Outgoing text is trimmed; ordinary prose stays unchanged.
				const trailingTrigger = findActiveTrigger(promptSuggestion.text, promptSuggestion.text.length);
				selection.insertText(promptSuggestion.text + (trailingTrigger ? " " : ""));
				if (event) {
					completionHandledEvents.add(event);
					event.preventDefault();
				}
				return true;
			}
			const value = onComplete(snapshot, key);
			if (snapshot.trigger && value) {
				if (!$insertComposerToken(snapshot.trigger, value)) return false;
				if (event) {
					completionHandledEvents.add(event);
					event.preventDefault();
				}
				return true;
			}
			if (key === "Enter" && event && onEnter(snapshot, event)) {
				completionHandledEvents.add(event);
				event.preventDefault();
				return true;
			}
			return false;
		};
		const removeEnter = editor.registerCommand(
			KEY_ENTER_COMMAND,
			(event) => {
				// Claim only the editor command: the browser must still confirm the IME
				// candidate, without Lexical inserting a newline or selecting a suggestion.
				if (event?.isComposing || event?.keyCode === 229) return true;
				if (event?.shiftKey || editor.isComposing()) return false;
				if (complete(event, "Enter")) return true;
				return false;
			},
			COMMAND_PRIORITY_HIGH,
		);
		const removeTab = editor.registerCommand(
			KEY_TAB_COMMAND,
			(event) => complete(event, "Tab"),
			COMMAND_PRIORITY_HIGH,
		);
		const removeEscape = editor.registerCommand(KEY_ESCAPE_COMMAND, (event) => {
			if (!promptSuggestion || $serializeComposer() || event.isComposing || editor.isComposing()) return false;
			onDismissSuggestion();
			completionHandledEvents.add(event);
			event.preventDefault();
			event.stopPropagation();
			return true;
		}, COMMAND_PRIORITY_HIGH);
		return () => {
			removeEnter();
			removeTab();
			removeEscape();
		};
	}, [editor, enterSends, onComplete, onDismissSuggestion, onEnter, promptSuggestion]);

	// Markdown list keys. They run after send and completion above decline the
	// key, so they only ever replace a plain newline, Tab focus move or Backspace.
	useEffect(() => {
		const plainKey = (event: globalThis.KeyboardEvent | null): event is globalThis.KeyboardEvent =>
			event !== null && !event.defaultPrevented && !event.isComposing && event.keyCode !== 229 &&
			!editor.isComposing() && !event.metaKey && !event.ctrlKey && !event.altKey;
		const handled = (event: globalThis.KeyboardEvent) => {
			event.preventDefault();
			return true;
		};
		const removeEnter = editor.registerCommand(KEY_ENTER_COMMAND, (event) => {
			if (!plainKey(event)) return false;
			const caret = $caretLine();
			const action = caret && listEnter(caret.line, caret.column, caret.previous);
			if (!caret || !action) return false;
			if (action.kind === "exit") return $applyLinePrefixEdit(caret.node, caret.column, action.edit) && handled(event);
			$addUpdateTag(HISTORY_PUSH_TAG);
			caret.selection.insertRawText(action.text);
			return handled(event);
		}, COMMAND_PRIORITY_LOW);
		const removeTab = editor.registerCommand(KEY_TAB_COMMAND, (event) => {
			if (!plainKey(event)) return false;
			const caret = $caretLine();
			const edit = caret && listIndent(caret.line, caret.previous, event.shiftKey);
			return Boolean(caret && edit && $applyLinePrefixEdit(caret.node, caret.column, edit)) && handled(event);
		}, COMMAND_PRIORITY_LOW);
		const removeBackspace = editor.registerCommand(KEY_BACKSPACE_COMMAND, (event) => {
			if (!plainKey(event) || event.shiftKey) return false;
			// A marker always ends in a space or tab; skip reading the draft on every other Backspace.
			const selection = $getSelection();
			if (!$isRangeSelection(selection) || !/[ \t]/.test(selection.anchor.getNode().getTextContent()[selection.anchor.offset - 1] ?? "")) return false;
			const caret = $caretLine();
			const edit = caret && listBackspace(caret.line, caret.column, caret.previous);
			return Boolean(caret && edit && $applyLinePrefixEdit(caret.node, caret.column, edit)) && handled(event);
		}, COMMAND_PRIORITY_LOW);
		return () => {
			removeEnter();
			removeTab();
			removeBackspace();
		};
	}, [editor]);

	return null;
});

/**
 * Fades a changing placeholder (the orchestrator's start-up steps) out, swaps
 * the text, and fades it back in. Fast on purpose. The first text mounts
 * directly.
 */
const PLACEHOLDER_FADE_MS = 80;

function FadingPlaceholder({ text }: { text: string }) {
	const [shown, setShown] = useState(text);
	useEffect(() => {
		if (text === shown) return;
		const timer = setTimeout(() => setShown(text), PLACEHOLDER_FADE_MS);
		return () => clearTimeout(timer);
	}, [text, shown]);
	return (
		<span
			className="transition-opacity ease-out motion-reduce:transition-none"
			style={{ opacity: text === shown ? 1 : 0, transitionDuration: `${PLACEHOLDER_FADE_MS}ms` }}
		>
			{shown}
		</span>
	);
}

export const ComposerEditor = forwardRef<
	ComposerEditorHandle,
	{
		disabled?: boolean;
		enterSends: boolean;
		/** Hides the text while a send is in flight; the draft is still held for recovery. */
		concealed?: boolean;
		/** While dictating, the words arrive on their own: no caret, placeholder or suggestion. */
		dictating?: boolean;
		label: string;
		placeholder: string;
		promptSuggestion?: ComposerPromptSuggestion;
		menuOpen: boolean;
		menuId: string;
		activeIndex: number;
		images?: ComposerImage[];
		onChange: (snapshot: ComposerEditorSnapshot) => void;
		onComplete: (snapshot: ComposerEditorSnapshot, key: "Enter" | "Tab") => string | undefined;
		onEnter: (snapshot: ComposerEditorSnapshot, event: globalThis.KeyboardEvent) => boolean;
		onCompositionChange: (isComposing: boolean) => void;
		onKeyDown: (event: KeyboardEvent<HTMLDivElement>) => void;
		onPaste: (event: ClipboardEvent<HTMLDivElement>) => void;
	}
>(function ComposerEditor(
	{
		disabled,
		enterSends,
		concealed,
		dictating,
		label,
		placeholder,
		promptSuggestion,
		menuOpen,
		menuId,
		activeIndex,
		images = [],
		onChange,
		onComplete,
		onEnter,
		onCompositionChange,
		onKeyDown,
		onPaste,
	},
	ref,
) {
	const { t } = useTranslation();
	const suggestionDescriptionId = useId();
	const [dismissedSuggestionId, setDismissedSuggestionId] = useState<string>();
	const [empty, setEmpty] = useState(true);
	const suggestion = empty && !disabled && !concealed && !dictating && promptSuggestion?.id !== dismissedSuggestionId
		? promptSuggestion
		: undefined;
	const dismissSuggestion = useCallback(() => {
		if (promptSuggestion) {
			setDismissedSuggestionId(promptSuggestion.id);
			promptSuggestion.onDismiss?.();
		}
	}, [promptSuggestion]);
	const initialConfig = {
		namespace: "AOChatComposer",
		nodes: [ComposerTokenNode, DictationLetterNode],
		editable: !disabled,
		theme: { paragraph: "m-0" },
		onError(error: Error) {
			throw error;
		},
	};

	// Read at setText time, not render time, so a draft restore sees the
	// attachments it was restored with.
	const imagesRef = useRef(images);
	imagesRef.current = images;
	const attachedImages = useCallback(() => imagesRef.current.map((image) => image.path), []);

	const placeholderNode = useCallback(
		() => (
			<div className={cn(
				"py-1 text-base! leading-relaxed text-muted-foreground",
				COMPOSER_TEXT_INSET,
				suggestion
					? "min-h-[4.5rem] whitespace-pre-wrap break-words"
					// Same gutter as the field below, so the first typed character lands where the placeholder began.
					: cn("pointer-events-none absolute inset-x-0 top-0 overflow-hidden", COMPOSER_SCROLL_GUTTER),
			)}>
				{suggestion ? <span data-testid="prompt-suggestion">{suggestion.text}</span> : (
					// Both stay mounted in one grid cell, so turning dictation on or off crossfades them.
					<span className="grid *:col-start-1 *:row-start-1 *:transition-[opacity,filter] *:duration-300 *:ease-[cubic-bezier(0.23,1,0.32,1)] motion-reduce:*:transition-none">
						<span aria-hidden={dictating} className={dictating ? "opacity-0 blur-[2px]" : "opacity-100 blur-0"}>
							<FadingPlaceholder text={placeholder} />
						</span>
						<span aria-hidden={!dictating} className={dictating ? "opacity-100 blur-0" : "opacity-0 blur-[2px]"}>
							{t("chat.dictation.placeholder")}
						</span>
					</span>
				)}
			</div>
		),
		[dictating, placeholder, suggestion, t],
	);

	return (
		<LexicalComposer initialConfig={initialConfig}>
			<ComposerImages.Provider value={images}>
			<div className={cn("relative", suggestion && cn("max-h-40 overflow-y-auto overscroll-contain", COMPOSER_SCROLL_GUTTER))}>
				<PlainTextPlugin
					contentEditable={
						<ContentEditable
							aria-label={label}
							aria-placeholder={placeholder}
							aria-describedby={suggestion ? suggestionDescriptionId : undefined}
							placeholder={placeholderNode}
							aria-disabled={disabled || undefined}
							role="combobox"
							aria-expanded={menuOpen}
							aria-controls={menuOpen ? menuId : undefined}
							aria-activedescendant={
								menuOpen ? `${menuId}-option-${activeIndex}` : undefined
							}
							aria-autocomplete={suggestion ? "both" : "list"}
							onCompositionStart={() => onCompositionChange(true)}
							onCompositionEnd={() => onCompositionChange(false)}
							onKeyDown={(event) => {
								if (!completionHandledEvents.has(event.nativeEvent)) onKeyDown(event);
							}}
							onPasteCapture={(event) => {
								onPaste(event);
								if (event.defaultPrevented) event.stopPropagation();
							}}
							className={cn(
								"max-h-40 min-h-[4.5rem] w-full overflow-y-auto overscroll-contain bg-transparent py-1 text-base! leading-relaxed text-foreground caret-foreground outline-none selection:bg-foreground selection:text-on-foreground",
								COMPOSER_TEXT_INSET,
								COMPOSER_SCROLL_GUTTER,
								suggestion && "absolute inset-0 overflow-visible overscroll-auto",
								dictating && "caret-transparent",
								concealed ? "invisible" : disabled && "opacity-50",
							)}
						/>
					}
					ErrorBoundary={LexicalErrorBoundary}
				/>
				{concealed ? <div aria-hidden="true">{placeholderNode()}</div> : null}
				{suggestion ? (
					<span id={suggestionDescriptionId} className="sr-only">
						{t("chat.promptSuggestion.description", { prompt: suggestion.text })}
					</span>
				) : null}
				<HistoryPlugin />
				<EditorBridge
					ref={ref}
					disabled={disabled}
					enterSends={enterSends}
					promptSuggestion={suggestion}
					onDismissSuggestion={dismissSuggestion}
					onEmptyChange={setEmpty}
					attachedImages={attachedImages}
					onChange={onChange}
					onComplete={onComplete}
					onEnter={onEnter}
				/>
			</div>
			</ComposerImages.Provider>
		</LexicalComposer>
	);
});
