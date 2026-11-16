import type { Element, ElementContent, Root, Text } from "hast";

/** Map decoded visible text back to its bounded AST source segment. */
export function excerptSourceOffsets(raw: string, value: string, start: number, literal = false): number[] | undefined {
	const offsets = [start];
	let visible = "";
	for (let i = 0; i < raw.length;) {
		const begin = i;
		let token = raw[i++];
		if (!literal && token === "\\" && /[!"#$%&'()*+,\-./:;<=>?@[\]\\^_`{|}~]/.test(raw[i] ?? "")) token = raw[i++];
		else if (!literal && token === "&") {
			const entity = /^&(?:#\d+|#x[\da-f]+|[a-z][\da-z]+);/i.exec(raw.slice(begin))?.[0];
			if (entity) {
				const decoder = document.createElement("textarea");
				decoder.innerHTML = entity;
				if (decoder.value !== entity) {
					token = decoder.value;
					i = begin + entity.length;
				}
			}
		} else if (token === "\r") { if (raw[i] === "\n") i++; token = "\n"; }
		visible += token;
		for (let k = 0; k < token.length; k++) offsets.push(start + (k === token.length - 1 ? i : begin));
	}
	return visible === value ? offsets : undefined;
}

function textSpan(text: Text, base: number): ElementContent {
	const start = text.position?.start.offset;
	const end = text.position?.end.offset;
	if (start === undefined || end === undefined || !text.value.trim()) return text;
	return { type: "element", tagName: "span", properties: {
		"data-excerpt-start": base + start, "data-excerpt-end": base + end,
		"data-excerpt-kind": "text",
	}, children: [text] };
}

/** Preserve source positions before React custom renderers/highlighters transform it. */
export function rehypeExcerptSource({ offset }: { offset: number }) {
	return (tree: Root) => {
		function walk(parent: Root | Element) {
			const only = parent.children.length === 1 ? parent.children[0] : undefined;
			if (parent.type === "element" && only?.type === "text" &&
				["strong", "em", "del", "p", "li", "h1", "h2", "h3", "h4", "h5", "h6", "td", "th"].includes(parent.tagName)) {
				const mapped = textSpan(only, offset);
				if (mapped.type === "element") Object.assign(parent.properties, mapped.properties);
				return;
			}
			parent.children = parent.children.map(child => {
				if (child.type === "text") return textSpan(child, offset);
				if (child.type !== "element") return child;
				const start = child.position?.start.offset;
				const end = child.position?.end.offset;
				if (start !== undefined && end !== undefined) {
					child.properties["data-excerpt-start"] = offset + start;
					child.properties["data-excerpt-end"] = offset + end;
				}
				// Code text is transformed by React; its renderer owns the final mapping.
				if (child.tagName !== "pre" && child.tagName !== "code") walk(child);
				return child;
			}) as typeof parent.children;
		}
		walk(tree);
	};
}

export function excerptElementProps(node: Element | undefined) {
	return {
		"data-excerpt-start": node?.properties["data-excerpt-start"] as number | undefined,
		"data-excerpt-end": node?.properties["data-excerpt-end"] as number | undefined,
	};
}

/** Map code by source line: fences/indentation are never mistaken for code text. */
export function codeSourceOffsets(raw: string, code: string, start: number, fenced: boolean): number[] | undefined {
	let from = 0;
	if (fenced && /^\s*(?:`{3,}|~{3,})/.test(raw)) from = raw.indexOf("\n") + 1;
	else if (!fenced) {
		const fence = /^(`+)[\s\S]*\1$/.exec(raw);
		if (fence) {
			const length = fence[1].length;
			const body = raw.slice(length, -length);
			let visible = "";
			const map = [start + length];
			for (let i = 0; i < body.length; i++) {
				const character = body[i];
				if (character === "\r" && body[i + 1] === "\n") i++;
				visible += character === "\n" || character === "\r" ? " " : character;
				map.push(start + length + i + 1);
			}
			if (visible.startsWith(" ") && visible.endsWith(" ") && visible.trim()) {
				visible = visible.slice(1, -1); map.shift(); map.pop();
			}
			return visible === code ? map : undefined;
		}
	}
	const offsets: number[] = [];
	const lines = code.split("\n");
	for (let row = 0; row < lines.length; row++) {
		const newline = raw.indexOf("\n", from);
		const to = newline < 0 ? raw.length : newline;
		const rawLine = raw.slice(from, to).replace(/\r$/, "");
		const column = rawLine.indexOf(lines[row]);
		if (column < 0 || rawLine.slice(0, column).trim()) return undefined;
		const map = excerptSourceOffsets(rawLine.slice(column, column + lines[row].length), lines[row], start + from + column, true);
		if (!map) return undefined;
		if (row) offsets.push(start + from + column);
		for (const position of row ? map.slice(1) : map) offsets.push(position);
		from = to + 1;
	}
	return offsets.length === code.length + 1 ? offsets : undefined;
}
