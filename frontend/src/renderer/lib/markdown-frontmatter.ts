/** Plain-text name and description from a Markdown file's opening YAML frontmatter. */

const MAX_PREVIEW_CHARS = 280;

export type MarkdownFrontmatterStatus = "ok" | "absent" | "malformed";

export type MarkdownFrontmatter = {
	status: MarkdownFrontmatterStatus;
	name?: string;
	description?: string;
};

const OPENING = /^(?:\uFEFF)?---[ \t]*\r?\n/;

/** Hide opening YAML metadata in rendered documents, leaving incomplete blocks intact. */
export function stripMarkdownFrontmatter(content: string): string {
	const opening = OPENING.exec(content);
	if (!opening) return content;
	const rest = content.slice(opening[0].length);
	const closing = /(?:^|\r?\n)---[ \t]*(?:\r?\n|$)/.exec(rest);
	return closing ? rest.slice(closing.index + closing[0].length) : content;
}

const MARKDOWN_FILE = /\.(?:md|markdown)$/i;

export function isMarkdownFilePath(path: string): boolean {
	return MARKDOWN_FILE.test(path);
}

export function stripMarkdownPreviewAnchor(path: string): string {
	if (MARKDOWN_FILE.test(path)) return path;
	const hashIndex = path.lastIndexOf("#");
	if (hashIndex >= 0 && MARKDOWN_FILE.test(path.slice(0, hashIndex))) {
		return path.slice(0, hashIndex);
	}
	return path;
}

export function isMarkdownPreviewPath(path: string): boolean {
	// A caller may already have decoded the target into a real filename, where
	// a literal "#" or "?" is part of the name. Check the whole reference first
	// so those files still resolve by their extension.
	if (MARKDOWN_FILE.test(path)) return true;
	const withoutQuery = path.split("?", 1)[0] ?? "";
	if (MARKDOWN_FILE.test(withoutQuery)) return true;
	const hashIndex = withoutQuery.lastIndexOf("#");
	if (hashIndex < 0) return false;
	return MARKDOWN_FILE.test(withoutQuery.slice(0, hashIndex));
}

export function parseMarkdownFrontmatter(content: string): MarkdownFrontmatter {
	if (!OPENING.test(content)) return { status: "absent" };
	const bodyStart = OPENING.exec(content)?.[0].length ?? 0;
	const rest = content.slice(bodyStart);
	const close = /\r?\n---[ \t]*(?:\r?\n|$)/.exec(rest);
	if (!close || close.index == null) return { status: "malformed" };
	const parsed = parseFields(rest.slice(0, close.index));
	if (!parsed) return { status: "malformed" };
	const name = present(parsed.name);
	const description = present(parsed.description);
	return { status: "ok", name, description };
}

export function clampPreviewText(value: string): string {
	const flat = value.replace(/\s+/g, " ").trim();
	if (flat.length <= MAX_PREVIEW_CHARS) return flat;
	return `${flat.slice(0, MAX_PREVIEW_CHARS - 3)}...`;
}

function present(value: string | undefined): string | undefined {
	const trimmed = value?.trim();
	return trimmed ? trimmed : undefined;
}

function parseFields(body: string): { name?: string; description?: string } | null {
	const lines = body.split(/\r?\n/);
	const fields: { name?: string; description?: string } = {};
	let index = 0;
	while (index < lines.length) {
		const line = lines[index] ?? "";
		if (line.trim() === "" || /^\s*#/.test(line)) {
			index += 1;
			continue;
		}
		if (/^\s/.test(line)) return null;
		const match = /^([A-Za-z][A-Za-z0-9_-]*):[ \t]*(.*)$/.exec(line);
		if (!match) return null;
		const key = match[1] ?? "";
		const raw = match[2] ?? "";
		const known = key === "name" || key === "description";
		if (/^(?:[|>][+-]?)$/.test(raw.trim())) {
			const block = readBlock(lines, index + 1);
			if (!block) return null;
			if (known) fields[key] = block.value;
			index = block.next;
			continue;
		}
		if (raw.trim() === "" && index + 1 < lines.length && /^\s+\S/.test(lines[index + 1] ?? "")) {
			if (known) return null;
			index = skipIndented(lines, index + 1);
			continue;
		}
		if (!known) {
			index += 1;
			continue;
		}
		const scalar = readScalar(raw);
		if (scalar == null) return null;
		fields[key] = scalar;
		index += 1;
	}
	return fields;
}

function readBlock(lines: string[], start: number): { value: string; next: number } | null {
	const collected: string[] = [];
	let index = start;
	let indent: number | undefined;
	while (index < lines.length) {
		const line = lines[index] ?? "";
		if (line.trim() === "") {
			collected.push("");
			index += 1;
			continue;
		}
		const leading = line.match(/^[ \t]*/)?.[0].length ?? 0;
		if (leading === 0) break;
		if (indent == null) indent = leading;
		if (leading < indent) return null;
		collected.push(line.slice(indent));
		index += 1;
	}
	if (indent == null) return { value: "", next: index };
	return { value: collected.join("\n").trim(), next: index };
}

function skipIndented(lines: string[], start: number): number {
	let index = start;
	while (index < lines.length && (/^\s/.test(lines[index] ?? "") || (lines[index] ?? "").trim() === "")) index += 1;
	return index;
}

function readScalar(raw: string): string | null {
	const value = raw.trim();
	if (value === "" || value === "~" || value === "null" || value === "Null" || value === "NULL") return "";
	if (value.startsWith("\"") || value.startsWith("'")) return unquote(value);
	if (/^[[{&*!|>]/.test(value)) return null;
	return value;
}

function unquote(value: string): string | null {
	const quote = value[0];
	if (!quote) return null;
	if (!value.endsWith(quote) || value.length < 2) return null;
	const inner = value.slice(1, -1);
	if (quote === "'") return inner.replace(/''/g, "'");
	let out = "";
	for (let index = 0; index < inner.length; index += 1) {
		const char = inner[index];
		if (char !== "\\") {
			out += char;
			continue;
		}
		const next = inner[index + 1];
		if (next == null) return null;
		index += 1;
		if (next === "n") out += "\n";
		else if (next === "t") out += "\t";
		else if (next === "\"" || next === "\\" || next === "/") out += next;
		else return null;
	}
	return out;
}
