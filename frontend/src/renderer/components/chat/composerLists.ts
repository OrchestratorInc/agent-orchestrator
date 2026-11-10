/**
 * Markdown list keys for the plain-text composer. The draft stays plain
 * markdown text: these only decide what a key does on a line that is a list
 * item (`- `, `* `, `+ `, `1. `, `1) `, optionally with a `[ ] ` task box).
 */

const LIST_ITEM = /^([ \t]*)([-*+]|(\d{1,9})([.)]))([ \t]+)(\[[ xX]\][ \t]+)?/;

type ListItem = {
	indent: string;
	/** `-`, `*` or `+`; undefined for a numbered item. */
	bullet?: string;
	number?: number;
	delimiter?: string;
	task: boolean;
	/** Where the item's text starts, not counting a task box: nested items line up there. */
	textColumn: number;
	/** Everything before the item's text: indent, marker, spacing and task box. */
	prefix: string;
};

function parseListItem(line: string): ListItem | undefined {
	const match = LIST_ITEM.exec(line);
	if (!match) return undefined;
	const [prefix, indent, marker, number, delimiter, spacing, task] = match;
	const shared = { indent, task: Boolean(task), textColumn: indent.length + marker.length + spacing.length, prefix };
	return number === undefined ? { ...shared, bullet: marker } : { ...shared, number: Number(number), delimiter };
}

function itemPrefix(item: ListItem, indent: string, number = item.number): string {
	const marker = item.bullet ?? `${number}${item.delimiter}`;
	return `${indent}${marker} ${item.task ? "[ ] " : ""}`;
}

/** Replace the first `remove` characters of the line with `insert`. */
export type LinePrefixEdit = { remove: number; insert: string };

export type ListEnter =
	/** Insert this at the caret: a newline and the next item's marker. */
	| { kind: "continue"; text: string }
	/** The item was empty: drop its marker and end the list. */
	| { kind: "exit"; edit: LinePrefixEdit };

/** What the newline key does at `column` of `line`; undefined leaves it a plain newline. */
export function listEnter(line: string, column: number): ListEnter | undefined {
	const item = parseListItem(line);
	if (!item || column < item.prefix.length) return undefined;
	if (line.length === item.prefix.length) return { kind: "exit", edit: { remove: line.length, insert: "" } };
	return { kind: "continue", text: `\n${itemPrefix(item, item.indent, item.number === undefined ? undefined : item.number + 1)}` };
}

/** Backspace right after an item's marker removes the marker and keeps the text (and its indent). */
export function listBackspace(line: string, column: number): LinePrefixEdit | undefined {
	const item = parseListItem(line);
	if (!item || column !== item.prefix.length) return undefined;
	return { remove: item.prefix.length, insert: item.indent };
}

/**
 * The number an item gets at `indent` below `previous`: one more than the
 * sibling above it, or 1 when it starts a new list there.
 */
function numberAt(previous: string[], indent: number): number {
	for (let index = previous.length - 1; index >= 0; index--) {
		const item = parseListItem(previous[index]);
		if (!item) {
			if (previous[index].trim()) return 1;
			continue;
		}
		if (item.indent.length === indent) return item.number === undefined ? 1 : item.number + 1;
		if (item.indent.length < indent) return 1;
	}
	return 1;
}

/**
 * Tab nests the item under the one above it at its own level; Shift+Tab moves it
 * out to its parent's level, and out of the list from the top level. `previous`
 * is the draft's lines above this one. Undefined leaves Tab to move focus.
 */
export function listIndent(line: string, previous: string[], outdent: boolean): LinePrefixEdit | undefined {
	const item = parseListItem(line);
	if (!item) return undefined;
	const current = item.indent.length;
	let target: string | undefined;
	for (let index = previous.length - 1; index >= 0; index--) {
		const above = parseListItem(previous[index]);
		if (!above) {
			// A blank line or an item's indented text continues the list; prose ends it.
			if (!previous[index].trim() || /^[ \t]/.test(previous[index])) continue;
			break;
		}
		if (outdent ? above.indent.length < current : above.indent.length === current) {
			// Nested items line up with their parent's text.
			target = outdent ? above.indent : " ".repeat(above.textColumn);
			break;
		}
	}
	if (outdent) {
		if (current === 0) return { remove: item.prefix.length, insert: "" };
		target ??= "";
	} else if (target === undefined || target.length <= current) {
		return undefined;
	}
	return { remove: item.prefix.length, insert: itemPrefix(item, target, item.number === undefined ? undefined : numberAt(previous, target.length)) };
}
