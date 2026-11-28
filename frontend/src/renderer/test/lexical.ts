import { act } from "@testing-library/react";
import {
	$getRoot,
	$isElementNode,
	$isTextNode,
	CONTROLLED_TEXT_INSERTION_COMMAND,
	type LexicalEditor,
} from "lexical";

function lexicalEditor(field: HTMLElement): LexicalEditor {
	const editor = (field as HTMLElement & { __lexicalEditor?: LexicalEditor }).__lexicalEditor;
	if (!editor) throw new Error("Expected a Lexical editor");
	return editor;
}

/** JSDOM does not perform native contenteditable insertion, so drive Lexical directly. */
export async function typeInLexicalEditor(field: HTMLElement, text: string): Promise<void> {
	const editor = lexicalEditor(field);
	await act(async () => {
		field.focus();
		editor.update(() => $getRoot().selectEnd(), { discrete: true });
		for (const character of text) {
			editor.dispatchCommand(CONTROLLED_TEXT_INSERTION_COMMAND, character);
		}
	});
}

/**
 * Backspace at the end of the text: its last character goes. Lexical's own
 * delete moves through the DOM selection, which JSDOM cannot, so the text
 * node is changed directly.
 */
export async function deleteBackwardInLexicalEditor(field: HTMLElement): Promise<void> {
	const editor = lexicalEditor(field);
	await act(async () => {
		field.focus();
		editor.update(() => {
			const last = $getRoot().getLastDescendant();
			if ($isTextNode(last)) {
				const text = last.getTextContent();
				if (text.length <= 1) last.remove();
				else last.setTextContent(text.slice(0, -1));
			}
			$getRoot().selectEnd();
		}, { discrete: true });
	});
}

/** Reproduce a key pressed before React paints Lexical's latest text update. */
export async function typeAndPressInLexicalEditor(
	field: HTMLElement,
	text: string,
	key: string,
): Promise<void> {
	const editor = lexicalEditor(field);
	await act(async () => {
		field.focus();
		editor.update(() => $getRoot().selectEnd(), { discrete: true });
		for (const character of text) {
			editor.dispatchCommand(CONTROLLED_TEXT_INSERTION_COMMAND, character);
		}
		field.dispatchEvent(new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true }));
	});
}

export async function placeLexicalCaret(field: HTMLElement, offset: number): Promise<void> {
	const editor = lexicalEditor(field);
	await act(async () => {
		editor.update(
			() => {
				const node = $getRoot().getFirstDescendant();
				if (!$isTextNode(node)) throw new Error("Expected composer text");
				node.select(offset, offset);
			},
			{ discrete: true },
		);
	});
}

/** Put a collapsed caret at `offset` in one line (paragraph) of a multiline draft. */
export async function placeLexicalCaretInLine(field: HTMLElement, line: number, offset: number): Promise<void> {
	const editor = lexicalEditor(field);
	await act(async () => {
		editor.update(
			() => {
				const paragraph = $getRoot().getChildAtIndex(line);
				const node = $isElementNode(paragraph) ? paragraph.getFirstDescendant() : null;
				if (!$isTextNode(node)) throw new Error("Expected composer text on that line");
				node.select(offset, offset);
			},
			{ discrete: true },
		);
	});
}

/** Put a collapsed caret after the last character of the whole draft. */
export async function placeLexicalCaretAtEnd(field: HTMLElement): Promise<void> {
	const editor = lexicalEditor(field);
	await act(async () => {
		editor.update(() => $getRoot().selectEnd(), { discrete: true });
	});
}

export function lexicalEditorText(field: HTMLElement): string {
	return lexicalEditor(field)
		.getEditorState()
		.read(() => $getRoot().getChildren().map((child) => child.getTextContent()).join("\n"));
}

/** Empties the editor the way deleting all of its text would. */
export function clearLexicalEditor(field: HTMLElement): void {
	lexicalEditor(field).update(() => $getRoot().clear(), { discrete: true });
}

/**
 * What the chat composer's empty box actually draws, read from its placeholder
 * nodes rather than its aria attribute: the next-message suggestion, the hint
 * (marked while it fades out), or null while the box holds text.
 */
export function drawnComposerPlaceholder(field: HTMLElement): string | null {
	const box = field.parentElement;
	if (!box) return null;
	const suggestion = box.querySelector('[data-testid="prompt-suggestion"]');
	if (suggestion) return `suggestion: ${suggestion.textContent}`;
	const hint = box.querySelector<HTMLElement>('span[aria-hidden="false"] > span');
	if (!hint) return null;
	return hint.style.opacity === "0" ? `fading: ${hint.textContent}` : hint.textContent;
}
