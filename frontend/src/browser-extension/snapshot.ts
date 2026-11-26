// Accessibility-style page snapshot for the AO Browser Bridge content script.
// Runs in the extension's isolated world, so page JavaScript cannot observe
// it. Output matches agent-browser's compact format (`- role "name" [ref=eN]`)
// and refs map, so `ao browser act` and agent prompts work unchanged.

import { BridgeError } from "./protocol";

export type RefInfo = { role: string; name: string };
export type SnapshotResult = { snapshot: string; refs: Record<string, RefInfo>; truncated: boolean };
export type SnapshotOptions = { interactive?: boolean; maxLines?: number };

const MAX_NAME = 120;
const MAX_TEXT = 200;
const MAX_VALUE = 80;
const DEFAULT_MAX_LINES = 1_500;

export const INTERACTIVE_ROLES = new Set([
	"button",
	"link",
	"textbox",
	"searchbox",
	"checkbox",
	"radio",
	"combobox",
	"listbox",
	"option",
	"slider",
	"spinbutton",
	"switch",
	"tab",
	"menuitem",
	"menuitemcheckbox",
	"menuitemradio",
	"treeitem",
]);

// Roles whose accessible name comes from their content.
const NAME_FROM_CONTENT = new Set([
	"button",
	"link",
	"heading",
	"tab",
	"menuitem",
	"menuitemcheckbox",
	"menuitemradio",
	"option",
	"treeitem",
	"cell",
	"columnheader",
	"rowheader",
	"switch",
	"checkbox",
	"radio",
	"tooltip",
]);

// Non-interactive roles worth a line in the full snapshot.
const STRUCTURAL_ROLES = new Set([
	"heading",
	"navigation",
	"main",
	"banner",
	"contentinfo",
	"complementary",
	"region",
	"form",
	"search",
	"dialog",
	"alertdialog",
	"alert",
	"list",
	"listitem",
	"table",
	"row",
	"cell",
	"columnheader",
	"rowheader",
	"img",
	"iframe",
	"group",
	"tablist",
	"tabpanel",
	"menu",
	"menubar",
	"tree",
	"progressbar",
	"status",
	"paragraph",
]);

// Elements whose refs are useful even outside interactive mode.
const REF_ROLES = new Set([...INTERACTIVE_ROLES, "heading", "img", "iframe"]);

/** Stable element refs for one document. */
export class RefRegistry {
	private readonly byElement = new WeakMap<Element, string>();
	private readonly byRef = new Map<string, WeakRef<Element>>();
	private next = 1;

	refFor(element: Element): string {
		const existing = this.byElement.get(element);
		if (existing) return existing;
		const ref = `e${this.next++}`;
		this.byElement.set(element, ref);
		this.byRef.set(ref, new WeakRef(element));
		return ref;
	}

	resolve(ref: string): Element {
		const element = this.byRef.get(ref)?.deref();
		if (!element || !element.isConnected) {
			throw new BridgeError("STALE_REFERENCE", `Element ${ref} is no longer on the page; take a new snapshot`);
		}
		return element;
	}
}

function normalize(text: string): string {
	return text.replace(/\s+/g, " ").trim();
}

function truncate(text: string, max: number): string {
	return text.length > max ? `${text.slice(0, max - 1)}…` : text;
}

function quote(text: string): string {
	return `"${text.replace(/\\/g, "\\\\").replace(/"/g, '\\"')}"`;
}

function inputType(element: Element): string {
	return (element.getAttribute("type") ?? "text").toLowerCase();
}

/** The element's ARIA role: explicit role first, then the HTML-AAM default. */
export function roleOf(element: Element): string {
	const explicit = element.getAttribute("role")?.trim().split(/\s+/)[0]?.toLowerCase();
	if (explicit && explicit !== "presentation" && explicit !== "none") return explicit;
	const tag = element.tagName.toLowerCase();
	switch (tag) {
		case "a":
		case "area":
			return element.hasAttribute("href") ? "link" : "";
		case "button":
		case "summary":
			return "button";
		case "input": {
			const type = inputType(element);
			if (["button", "submit", "reset", "image"].includes(type)) return "button";
			if (type === "checkbox") return element.getAttribute("switch") !== null ? "switch" : "checkbox";
			if (type === "radio") return "radio";
			if (type === "range") return "slider";
			if (type === "number") return "spinbutton";
			if (type === "search") return element.hasAttribute("list") ? "combobox" : "searchbox";
			if (type === "hidden" || type === "file" || type === "color") return type === "file" ? "button" : "";
			return element.hasAttribute("list") ? "combobox" : "textbox";
		}
		case "textarea":
			return "textbox";
		case "select":
			return element.hasAttribute("multiple") || Number(element.getAttribute("size") ?? "0") > 1 ? "listbox" : "combobox";
		case "option":
			return "option";
		case "img":
			return element.getAttribute("alt") === "" ? "" : "img";
		case "h1":
		case "h2":
		case "h3":
		case "h4":
		case "h5":
		case "h6":
			return "heading";
		case "nav":
			return "navigation";
		case "main":
			return "main";
		case "header":
			return element.closest("article,aside,main,nav,section") ? "" : "banner";
		case "footer":
			return element.closest("article,aside,main,nav,section") ? "" : "contentinfo";
		case "aside":
			return "complementary";
		case "form":
			return accessibleNameFromAttributes(element) ? "form" : "";
		case "section":
			return accessibleNameFromAttributes(element) ? "region" : "";
		case "search":
			return "search";
		case "dialog":
			return "dialog";
		case "ul":
		case "ol":
		case "menu":
			return "list";
		case "li":
			return "listitem";
		case "table":
			return "table";
		case "tr":
			return "row";
		case "td":
			return "cell";
		case "th":
			return element.getAttribute("scope") === "row" ? "rowheader" : "columnheader";
		case "fieldset":
		case "details":
			return "group";
		case "progress":
			return "progressbar";
		case "iframe":
			return "iframe";
		case "p":
			return "paragraph";
		default:
			if (element.getAttribute("contenteditable") === "true" || element.getAttribute("contenteditable") === "") return "textbox";
			return "";
	}
}

function isHidden(element: Element): boolean {
	if (element.getAttribute("aria-hidden") === "true") return true;
	if ((element as HTMLElement).hidden) return true;
	if (element.tagName === "INPUT" && inputType(element) === "hidden") return true;
	const view = element.ownerDocument.defaultView;
	if (!view) return false;
	const style = view.getComputedStyle(element);
	return style.display === "none" || style.visibility === "hidden" || style.visibility === "collapse";
}

function textOf(node: Node, depth = 0, skip?: Element): string {
	if (depth > 40 || node === skip) return "";
	if (node.nodeType === Node.TEXT_NODE) return node.textContent ?? "";
	if (node.nodeType !== Node.ELEMENT_NODE) return "";
	const element = node as Element;
	if (isHidden(element)) return "";
	const tag = element.tagName.toLowerCase();
	if (tag === "img") return element.getAttribute("alt") ?? "";
	if (tag === "script" || tag === "style" || tag === "noscript" || tag === "template") return "";
	const label = element.getAttribute("aria-label");
	if (label && depth > 0) return label;
	let text = "";
	for (const child of childNodes(element)) text += ` ${textOf(child, depth + 1, skip)}`;
	return text;
}

function accessibleNameFromAttributes(element: Element): string {
	const labelledBy = element.getAttribute("aria-labelledby");
	if (labelledBy) {
		const document = element.ownerDocument;
		const text = labelledBy
			.split(/\s+/)
			.map((id) => document.getElementById(id))
			.filter((node): node is HTMLElement => Boolean(node))
			.map((node) => textOf(node))
			.join(" ");
		if (normalize(text)) return normalize(text);
	}
	const label = element.getAttribute("aria-label");
	return label ? normalize(label) : "";
}

function labelText(element: Element): string {
	const document = element.ownerDocument;
	const parts: string[] = [];
	const id = element.getAttribute("id");
	if (id) {
		for (const label of Array.from(document.querySelectorAll("label"))) {
			if (label.getAttribute("for") === id) parts.push(textOf(label));
		}
	}
	const wrapping = element.closest("label");
	// Exclude the control itself (for example a select's options) from a
	// wrapping label's text.
	if (wrapping) parts.push(textOf(wrapping, 0, element));
	return normalize(parts.join(" "));
}

/** A practical subset of the accessible-name computation. */
export function accessibleName(element: Element, role = roleOf(element)): string {
	const fromAttributes = accessibleNameFromAttributes(element);
	if (fromAttributes) return truncate(fromAttributes, MAX_NAME);
	const tag = element.tagName.toLowerCase();
	let name = "";
	if (tag === "input" || tag === "textarea" || tag === "select") {
		const type = inputType(element);
		if (tag === "input" && ["submit", "reset", "button"].includes(type)) {
			name = element.getAttribute("value") ?? (type === "submit" ? "Submit" : type === "reset" ? "Reset" : "");
		} else if (tag === "input" && type === "image") {
			name = element.getAttribute("alt") ?? element.getAttribute("value") ?? "Submit";
		} else {
			name = labelText(element) || element.getAttribute("placeholder") || "";
		}
	} else if (tag === "img" || tag === "area") {
		name = element.getAttribute("alt") ?? "";
	} else if (tag === "iframe") {
		name = element.getAttribute("title") ?? element.getAttribute("name") ?? "";
	} else if (tag === "fieldset") {
		const legend = element.querySelector(":scope > legend");
		name = legend ? textOf(legend) : "";
	} else if (tag === "table") {
		const caption = element.querySelector(":scope > caption");
		name = caption ? textOf(caption) : "";
	}
	if (!normalize(name) && NAME_FROM_CONTENT.has(role)) name = textOf(element);
	if (!normalize(name)) name = element.getAttribute("title") ?? "";
	return truncate(normalize(name), MAX_NAME);
}

function states(element: Element, role: string): string[] {
	const out: string[] = [];
	const level = /^h([1-6])$/i.exec(element.tagName)?.[1] ?? element.getAttribute("aria-level");
	if (role === "heading" && level) out.push(`level=${level}`);
	const input = element as HTMLInputElement;
	if (role === "checkbox" || role === "radio" || role === "switch" || role.startsWith("menuitem")) {
		const aria = element.getAttribute("aria-checked");
		const checked = typeof input.checked === "boolean" && element.tagName === "INPUT" ? input.checked : aria === "true";
		if (checked) out.push("checked");
		if (aria === "mixed") out.push("mixed");
	}
	if ((element as HTMLButtonElement).disabled || element.getAttribute("aria-disabled") === "true") out.push("disabled");
	const expanded = element.getAttribute("aria-expanded");
	if (expanded === "true") out.push("expanded");
	if (expanded === "false") out.push("collapsed");
	if (element.getAttribute("aria-selected") === "true" || (element.tagName === "OPTION" && (element as HTMLOptionElement).selected)) {
		out.push("selected");
	}
	if (element.getAttribute("aria-pressed") === "true") out.push("pressed");
	if ((element as HTMLInputElement).required || element.getAttribute("aria-required") === "true") out.push("required");
	return out;
}

function valueOf(element: Element, role: string): string | undefined {
	if (role !== "textbox" && role !== "searchbox" && role !== "combobox" && role !== "spinbutton" && role !== "slider") return undefined;
	const tag = element.tagName;
	if (tag === "INPUT" && inputType(element) === "password") return undefined;
	if (tag === "SELECT") {
		const select = element as HTMLSelectElement;
		const option = select.selectedOptions?.[0];
		return option ? truncate(normalize(option.textContent ?? ""), MAX_VALUE) : undefined;
	}
	if (tag === "INPUT" || tag === "TEXTAREA") {
		const value = (element as HTMLInputElement).value;
		return value ? truncate(normalize(value), MAX_VALUE) : undefined;
	}
	return undefined;
}

function childNodes(element: Element): Node[] {
	if (element.tagName === "SLOT") {
		const assigned = (element as HTMLSlotElement).assignedNodes?.({ flatten: true }) ?? [];
		if (assigned.length > 0) return assigned;
	}
	const root = (element as HTMLElement).shadowRoot;
	if (root) return Array.from(root.childNodes);
	if (element.tagName === "TEMPLATE") return [];
	return Array.from(element.childNodes);
}

type Line = { depth: number; text: string };

/** Build the compact snapshot for a document. */
export function buildSnapshot(document: Document, registry: RefRegistry, options: SnapshotOptions = {}): SnapshotResult {
	const interactive = options.interactive === true;
	const maxLines = options.maxLines ?? DEFAULT_MAX_LINES;
	const lines: Line[] = [];
	const refs: Record<string, RefInfo> = {};
	let truncated = false;

	const emit = (depth: number, text: string): boolean => {
		if (lines.length >= maxLines) {
			truncated = true;
			return false;
		}
		lines.push({ depth: interactive ? 0 : depth, text });
		return true;
	};

	const visit = (node: Node, depth: number): void => {
		if (truncated) return;
		if (node.nodeType === Node.TEXT_NODE) {
			if (interactive) return;
			const text = normalize(node.textContent ?? "");
			if (text) emit(depth, `- text: ${truncate(text, MAX_TEXT)}`);
			return;
		}
		if (node.nodeType !== Node.ELEMENT_NODE) return;
		const element = node as Element;
		const tag = element.tagName.toLowerCase();
		if (tag === "script" || tag === "style" || tag === "noscript" || tag === "template" || tag === "head") return;
		if (isHidden(element)) return;
		if (tag === "label") {
			// A label's text is its control's name; keep only the controls.
			for (const child of childNodes(element)) visitControlsOnly(child, depth);
			return;
		}

		const role = roleOf(element);
		const isInteractive = INTERACTIVE_ROLES.has(role);
		const include = interactive ? isInteractive || role === "iframe" : isInteractive || STRUCTURAL_ROLES.has(role);
		let childDepth = depth;
		if (include) {
			const name = accessibleName(element, role);
			// Unnamed generic structure adds noise without helping navigation.
			const keep = isInteractive || role === "iframe" || name || ["list", "listitem", "table", "row", "navigation", "main", "banner", "contentinfo", "dialog", "form", "paragraph"].includes(role);
			if (keep) {
				let line = `- ${role}`;
				if (name) line += ` ${quote(name)}`;
				for (const state of states(element, role)) line += ` [${state}]`;
				const value = valueOf(element, role);
				if (value !== undefined && value !== name) line += `: ${quote(value)}`;
				if (REF_ROLES.has(role)) {
					const ref = registry.refFor(element);
					refs[ref] = { role, name };
					line += ` [ref=${ref}]`;
				}
				if (role === "paragraph" && !name) {
					// Summarize running text in one line, then keep any links
					// or controls inside it addressable.
					const text = normalize(textOf(element));
					if (text && !emit(depth, `- paragraph: ${truncate(text, MAX_TEXT)}`)) return;
					for (const child of childNodes(element)) visitControlsOnly(child, depth + 1);
					return;
				}
				if (!emit(depth, line)) return;
				childDepth = depth + 1;
				if (NAME_FROM_CONTENT.has(role) || isInteractive) {
					// Content already summarized in the name; still surface nested controls.
					for (const child of childNodes(element)) visitControlsOnly(child, childDepth);
					return;
				}
			}
		}
		for (const child of childNodes(element)) visit(child, childDepth);
	};

	const visitControlsOnly = (node: Node, depth: number): void => {
		if (node.nodeType !== Node.ELEMENT_NODE || truncated) return;
		const element = node as Element;
		if (isHidden(element)) return;
		const role = roleOf(element);
		if (INTERACTIVE_ROLES.has(role)) {
			visit(element, depth);
			return;
		}
		for (const child of childNodes(element)) visitControlsOnly(child, depth);
	};

	const root = document.body ?? document.documentElement;
	if (root) visit(root, 0);
	const text = lines.map((line) => `${"  ".repeat(line.depth)}${line.text}`).join("\n");
	return {
		snapshot: truncated ? `${text}\n- text: (snapshot truncated after ${maxLines} lines)` : text,
		refs,
		truncated,
	};
}
