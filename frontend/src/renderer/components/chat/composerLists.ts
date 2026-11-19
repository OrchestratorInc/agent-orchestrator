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
	/** The item's own box, `[ ]` or `[x]`, when it is a task. */
	task?: string;
	/** Where the item's text starts, not counting a task box: nested items line up there. */
	textColumn: number;
	/** Everything before the item's text: indent, marker, spacing and task box. */
	prefix: string;
};

function parseListItem(line: string): ListItem | undefined {
	const match = LIST_ITEM.exec(line);
	if (!match) return undefined;
	const [prefix, indent, marker, number, delimiter, spacing, task] = match;
	const shared = { indent, task: task?.trimEnd(), textColumn: indent.length + marker.length + spacing.length, prefix };
	return number === undefined ? { ...shared, bullet: marker } : { ...shared, number: Number(number), delimiter };
}

/** The item's marker at `indent`. A new item gets an empty box; a moved one keeps its own. */
function itemPrefix(item: ListItem, indent: string, number: number | undefined, box: "new" | "keep"): string {
	const marker = item.bullet ?? `${number}${item.delimiter}`;
	return `${indent}${marker} ${item.task ? `${box === "new" ? "[ ]" : item.task} ` : ""}`;
}

/** True when the line after `previous` is inside an open ``` or ~~~ fence, where list keys must not touch code. */
function insideFence(previous: string[]): boolean {
	let open: string | undefined;
	for (const line of previous) {
		const fence = /^ {0,3}(`{3,}|~{3,})/.exec(line)?.[1];
		if (!fence) continue;
		if (open === undefined) open = fence;
		else if (fence[0] === open[0] && fence.length >= open.length) open = undefined;
	}
	return open !== undefined;
}

/** Replace the first `remove` characters of the line with `insert`. */
export type LinePrefixEdit = { remove: number; insert: string };

export type ListEnter =
	/** Insert this at the caret: a newline and the next item's marker. */
	| { kind: "continue"; text: string }
	/** An empty item under another item: drop its marker and end the list. */
	| { kind: "exit"; edit: LinePrefixEdit };

/**
 * What the newline key does at `column` of `line`, below the draft's `previous`
 * lines; undefined leaves it a plain newline. An empty item only ends a list
 * that is already going: a `1. ` just typed on its own line is kept.
 */
export function listEnter(line: string, column: number, previous: string[]): ListEnter | undefined {
	const item = parseListItem(line);
	if (!item || column < item.prefix.length || insideFence(previous)) return undefined;
	if (line.length === item.prefix.length) {
		const above = previous.at(-1);
		return above !== undefined && parseListItem(above)
			? { kind: "exit", edit: { remove: line.length, insert: "" } }
			: undefined;
	}
	return { kind: "continue", text: `\n${itemPrefix(item, item.indent, item.number === undefined ? undefined : item.number + 1, "new")}` };
}

/** Backspace right after an item's marker removes the marker and keeps the text (and its indent). */
export function listBackspace(line: string, column: number, previous: string[]): LinePrefixEdit | undefined {
	const item = parseListItem(line);
	if (!item || column !== item.prefix.length || insideFence(previous)) return undefined;
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
	if (!item || insideFence(previous)) return undefined;
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
		// Reached this item's parent without a sibling above it: nothing to nest under.
		if (!outdent && above.indent.length < current) break;
	}
	if (outdent) {
		if (current === 0) return { remove: item.prefix.length, insert: "" };
		target ??= "";
	} else if (target === undefined || target.length <= current) {
		return undefined;
	}
	return { remove: item.prefix.length, insert: itemPrefix(item, target, item.number === undefined ? undefined : numberAt(previous, target.length), "keep") };
}
