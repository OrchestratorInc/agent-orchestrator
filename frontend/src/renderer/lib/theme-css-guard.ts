/**
 * Says whether theme CSS would fetch a file. A theme may only reference what
 * it carries: url() must hold a data: URL or a #fragment, and @import,
 * image-set(), src() and image() are refused outright, since each can name a
 * file through a bare string. Otherwise a shared or agent-made theme could
 * send what is on screen to another host.
 *
 * It reads the stylesheet the way a browser's tokenizer does (comments,
 * strings, escapes, url tokens) rather than searching the text, so an escaped
 * function name (`u\72l(`), a comment marker inside a string, or a url in a
 * value the browser parses late (`background: url(…) var(--x)`) cannot slip
 * past. The daemon runs the same scan (cssLoadsFile in
 * backend/internal/service/apptheme/cssguard.go); both are held to
 * testdata/css_guard_vectors.json there.
 */

const FORBIDDEN_FUNCTIONS = new Set(["image-set", "-webkit-image-set", "src", "image"]);

function isHex(c: string): boolean {
	return (c >= "0" && c <= "9") || (c >= "a" && c <= "f") || (c >= "A" && c <= "F");
}

function isSpace(c: string): boolean {
	return c === " " || c === "\t" || c === "\n" || c === "\r" || c === "\f";
}

function isNewline(c: string): boolean {
	return c === "\n" || c === "\r" || c === "\f";
}

function isDigit(c: string): boolean {
	return c >= "0" && c <= "9";
}

/** A character that can be part of an identifier; non-ASCII ones count, as in CSS. */
function isNameChar(c: string): boolean {
	return (c >= "a" && c <= "z") || (c >= "A" && c <= "Z") || isDigit(c) || c === "_" || c === "-" || c >= "\u0080";
}

/** A backslash that starts an escape: any not followed by a newline or the end. */
function isValidEscape(css: string, i: number): boolean {
	return css[i] === "\\" && i + 1 < css.length && !isNewline(css[i + 1]);
}

function isNameStart(css: string, i: number): boolean {
	const c = css[i];
	if (c === "-") {
		if (i + 1 >= css.length) return false;
		const next = css[i + 1];
		return next === "-" || (isNameChar(next) && !isDigit(next)) || isValidEscape(css, i + 1);
	}
	return (isNameChar(c) && !isDigit(c)) || isValidEscape(css, i);
}

/** Decodes the escape at css[i] (a backslash): its text and the index after it. */
function readEscape(css: string, start: number): [string, number] {
	let i = start + 1;
	if (i >= css.length) return ["", i];
	if (!isHex(css[i])) {
		// A character outside the BMP is two code units; keep them together.
		const point = css.codePointAt(i) ?? 0;
		const text = String.fromCodePoint(point);
		return [text, i + text.length];
	}
	let value = 0;
	let digits = 0;
	while (i < css.length && digits < 6 && isHex(css[i])) {
		value = value * 16 + Number.parseInt(css[i], 16);
		i++;
		digits++;
	}
	// One whitespace after a hex escape belongs to it.
	if (i < css.length && isSpace(css[i])) {
		if (css[i] === "\r" && css[i + 1] === "\n") i++;
		i++;
	}
	if (value === 0 || value > 0x10ffff) value = 0xfffd;
	return [String.fromCodePoint(value), i];
}

/** Reads an identifier starting at i, with its escapes decoded. */
function readName(css: string, start: number): [string, number] {
	let i = start;
	let name = "";
	while (i < css.length) {
		if (isNameChar(css[i])) {
			name += css[i];
			i++;
		} else if (isValidEscape(css, i)) {
			const [text, next] = readEscape(css, i);
			name += text;
			i = next;
		} else {
			break;
		}
	}
	return [name, i];
}

/**
 * Reads the string whose opening quote is at i: its decoded content and the
 * index after it. As in CSS, an unescaped newline ends the string, so the text
 * after it is read as code again.
 */
function readString(css: string, start: number): [string, number] {
	const quote = css[start];
	let i = start + 1;
	let content = "";
	while (i < css.length) {
		const c = css[i];
		if (c === quote) return [content, i + 1];
		if (isNewline(c)) return [content, i];
		if (c === "\\") {
			if (i + 1 >= css.length) return [content, i + 1];
			if (isNewline(css[i + 1])) {
				// A line continuation.
				if (css[i + 1] === "\r" && css[i + 2] === "\n") i++;
				i += 2;
				continue;
			}
			const [text, next] = readEscape(css, i);
			content += text;
			i = next;
			continue;
		}
		content += c;
		i++;
	}
	return [content, i];
}

/** Reads what a url( names, starting after the parenthesis. */
function readUrlTarget(css: string, start: number): [string, number] {
	let i = start;
	while (i < css.length && isSpace(css[i])) i++;
	if (css[i] === '"' || css[i] === "'") return readString(css, i);
	let target = "";
	while (i < css.length && css[i] !== ")") {
		if (isValidEscape(css, i)) {
			const [text, next] = readEscape(css, i);
			target += text;
			i = next;
			continue;
		}
		target += css[i];
		i++;
	}
	return [target, i];
}

/** The first way `css` would fetch a file, in words, or null when it cannot. */
export function themeCssLoadsFile(css: string): string | null {
	const n = css.length;
	let i = 0;
	while (i < n) {
		const c = css[i];
		if (c === "/" && css[i + 1] === "*") {
			const end = css.indexOf("*/", i + 2);
			if (end < 0) return null;
			i = end + 2;
		} else if (c === '"' || c === "'") {
			i = readString(css, i)[1];
		} else if (c === "#") {
			// A hash token (#fff, #id): its name is not a function name.
			i = readName(css, i + 1)[1];
		} else if (c === "@") {
			const [name, next] = readName(css, i + 1);
			i = next;
			if (name.toLowerCase() === "import") return "@import";
		} else if (isNameStart(css, i)) {
			const [name, next] = readName(css, i);
			i = next;
			if (css[i] !== "(") continue;
			const lower = name.toLowerCase();
			if (lower === "url") {
				const [target, after] = readUrlTarget(css, i + 1);
				i = after;
				const where = target.trim().toLowerCase();
				if (!where.startsWith("data:") && !where.startsWith("#")) return `url(${where.slice(0, 80)})`;
			} else if (FORBIDDEN_FUNCTIONS.has(lower)) {
				return `${lower}()`;
			}
		} else {
			i++;
		}
	}
	return null;
}
