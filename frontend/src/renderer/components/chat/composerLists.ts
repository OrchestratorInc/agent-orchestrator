const LIST_ITEM = /^([ \t]*)([-*+]|(\d{1,9})([.)]))([ \t]+)(\[[ xX]\][ \t]+)?/;

type ListItem = {
	indent: string;
	bullet?: string;
	number?: number;
	delimiter?: string;
	task?: string;
	textColumn: number;
	prefix: string;
};

function parseListItem(line: string): ListItem | undefined {
	const match = LIST_ITEM.exec(line);
	if (!match) return undefined;
	const [prefix, indent, marker, number, delimiter, spacing, task] = match;
	const shared = { indent, task: task?.trimEnd(), textColumn: indent.length + marker.length + spacing.length, prefix };
	return number === undefined ? { ...shared, bullet: marker } : { ...shared, number: Number(number), delimiter };
}

function itemPrefix(item: ListItem, indent: string, number: number | undefined, box: "new" | "keep"): string {
	const marker = item.bullet ?? `${number}${item.delimiter}`;
	return `${indent}${marker} ${item.task ? `${box === "new" ? "[ ]" : item.task} ` : ""}`;
}

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

export type LinePrefixEdit = { remove: number; insert: string };

export type ListEnter =
	| { kind: "continue"; text: string }
	| { kind: "exit"; edit: LinePrefixEdit };

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

export function listBackspace(line: string, column: number, previous: string[]): LinePrefixEdit | undefined {
	const item = parseListItem(line);
	if (!item || column !== item.prefix.length || insideFence(previous)) return undefined;
	return { remove: item.prefix.length, insert: item.indent };
}

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

export function listIndent(line: string, previous: string[], outdent: boolean): LinePrefixEdit | undefined {
	const item = parseListItem(line);
	if (!item || insideFence(previous)) return undefined;
	const current = item.indent.length;
	let target: string | undefined;
	for (let index = previous.length - 1; index >= 0; index--) {
		const above = parseListItem(previous[index]);
		if (!above) {
			if (!previous[index].trim() || /^[ \t]/.test(previous[index])) continue;
			break;
		}
		if (outdent ? above.indent.length < current : above.indent.length === current) {
			target = outdent ? above.indent : " ".repeat(above.textColumn);
			break;
		}
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
