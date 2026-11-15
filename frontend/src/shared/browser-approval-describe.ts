// Reads the facts the approval gate classifies a browser action by: what the
// target element is, whether it submits a form, and what that form collects.
// The daemon decides; this only reports.
//
// describeApprovalTarget runs inside the page. It must stay self-contained
// (no imports, no outer helpers) because the panel injects it as source text
// with approvalDescribeScript, and the Chrome extension calls it in its
// content script with the element a ref resolved to.

export type ApprovalDescribeRequest =
	/** The element that has focus, for Enter. */
	| { mode: "focused" }
	/** The visible control whose role and accessible name match, for panel refs. */
	| { mode: "match"; role: string; name: string }
	/** The element passed alongside, for extension refs. */
	| { mode: "element" };

export type ApprovalElementFacts = {
	role: string;
	name: string;
	tag: string;
	inputType?: string;
	href?: string;
	isSubmit: boolean;
	formAction?: string;
	formMethod?: string;
	formHasPayment: boolean;
	formHasPassword: boolean;
	/** The form sets a password (change or reset), unlike a sign-in. */
	formHasNewPassword: boolean;
	submitName?: string;
	rect?: { x: number; y: number; width: number; height: number };
};

export function describeApprovalTarget(
	doc: Document,
	request: ApprovalDescribeRequest,
	element?: Element | null,
): ApprovalElementFacts | null {
	const clean = (value: string | null | undefined, limit = 120): string =>
		(value ?? "").replace(/\s+/g, " ").trim().slice(0, limit);
	const normalized = (value: string): string =>
		// ASCII-only source: a raw noncharacter here once stopped Chrome loading
		// the extension's content script (source-encoding.test.ts guards it).
		value.toLowerCase().replace(/[^\p{L}\p{N}\p{M}]+/gu, " ").trim();
	const implicitRole = (el: Element): string => {
		const explicit = el.getAttribute("role");
		if (explicit) return explicit.split(/\s+/)[0].toLowerCase();
		const tag = el.tagName.toLowerCase();
		if (tag === "a" && el.hasAttribute("href")) return "link";
		if (tag === "button" || tag === "summary") return "button";
		if (tag === "textarea") return "textbox";
		if (tag === "select") return "combobox";
		if (tag === "input") {
			const type = ((el as HTMLInputElement).type || "text").toLowerCase();
			if (["submit", "button", "reset", "image"].includes(type)) return "button";
			if (type === "checkbox") return "checkbox";
			if (type === "radio") return "radio";
			if (type === "search") return "searchbox";
			return "textbox";
		}
		if ((el as HTMLElement).isContentEditable) return "textbox";
		return "";
	};
	const accessibleName = (el: Element): string => {
		const labelled = el.getAttribute("aria-labelledby");
		if (labelled) {
			const text = labelled
				.split(/\s+/)
				.map((id) => doc.getElementById(id)?.textContent ?? "")
				.join(" ");
			if (clean(text)) return clean(text);
		}
		const aria = clean(el.getAttribute("aria-label"));
		if (aria) return aria;
		const tag = el.tagName.toLowerCase();
		if (tag === "input") {
			const input = el as HTMLInputElement;
			if (["submit", "button", "reset"].includes(input.type)) return clean(input.value) || (input.type === "submit" ? "Submit" : "");
			if (input.type === "image") return clean(input.alt);
			const id = input.id;
			const label = (id && doc.querySelector(`label[for="${typeof CSS !== "undefined" && CSS.escape ? CSS.escape(id) : id.replace(/["\\]/g, "\\$&")}"]`)) || input.closest("label");
			if (label) return clean(label.textContent);
			return clean(input.placeholder) || clean(input.title);
		}
		if (tag === "textarea" || (el as HTMLElement).isContentEditable) {
			return clean(el.getAttribute("placeholder")) || clean(el.getAttribute("title")) || clean(el.getAttribute("data-placeholder"));
		}
		return clean((el as HTMLElement).innerText ?? el.textContent) || clean(el.getAttribute("title"));
	};
	const formOf = (el: Element): HTMLFormElement | null =>
		(el as HTMLInputElement).form ?? el.closest("form");
	const isSubmitControl = (el: Element): boolean => {
		const tag = el.tagName.toLowerCase();
		if (tag === "button") return ((el as HTMLButtonElement).type || "submit").toLowerCase() === "submit" && formOf(el) !== null;
		if (tag === "input") return ["submit", "image"].includes((el as HTMLInputElement).type) && formOf(el) !== null;
		return false;
	};
	const paymentField = (field: Element): boolean => {
		const autocomplete = (field.getAttribute("autocomplete") ?? "").toLowerCase();
		if (/\bcc-(number|csc|exp|name)/.test(autocomplete)) return true;
		const hint = `${field.getAttribute("name") ?? ""} ${field.id} ${field.getAttribute("placeholder") ?? ""} ${field.getAttribute("aria-label") ?? ""}`.toLowerCase();
		return /card.?num|cc.?num|credit.?card|\bcvc\b|\bcvv\b|security.?code|card.?number/.test(hint);
	};
	const visible = (el: Element): boolean => {
		const rect = el.getBoundingClientRect();
		const style = doc.defaultView?.getComputedStyle(el);
		return rect.width > 0 && rect.height > 0 && style?.visibility !== "hidden" && style?.display !== "none";
	};

	let target: Element | null = null;
	if (request.mode === "element") target = element ?? null;
	if (request.mode === "focused") {
		target = doc.activeElement;
		if (target === doc.body) target = null;
	}
	if (request.mode === "match") {
		const wantName = normalized(request.name);
		const wantRole = request.role.toLowerCase();
		const candidates = Array.from(
			doc.querySelectorAll(
				"button, a[href], input, textarea, select, summary, [role], [contenteditable=''], [contenteditable='true']",
			),
		);
		// The exact accessible name first. A normalized match ("Continue!" vs
		// "Continue") is used only when it is the only one: picking the first of
		// several could describe another control's form.
		const visibleCandidates = candidates.filter(visible);
		const pick = (matches: Element[]) => {
			const sameRole = matches.filter((candidate) => implicitRole(candidate) === wantRole);
			if (sameRole.length === 1) return sameRole[0]!;
			if (sameRole.length === 0 && matches.length === 1) return matches[0]!;
			return null;
		};
		const exact = visibleCandidates.filter((candidate) => accessibleName(candidate) === clean(request.name));
		target = exact.length > 0 ? pick(exact) : pick(visibleCandidates.filter((candidate) => normalized(accessibleName(candidate)) === wantName));
	}
	if (!target) return null;

	const tag = target.tagName.toLowerCase();
	const form = formOf(target);
	const role = implicitRole(target);
	const facts: ApprovalElementFacts = {
		role,
		name: accessibleName(target),
		tag,
		isSubmit: false,
		formHasPayment: false,
		formHasPassword: false,
		formHasNewPassword: false,
	};
	if (tag === "input") facts.inputType = (target as HTMLInputElement).type;
	if (tag === "a") facts.href = clean((target as HTMLAnchorElement).href, 500);
	if (form) {
		// Controls are collected by their own form owner, in document order:
		// that includes fields outside the form tag that join it with
		// form="id" and image buttons (which form.elements leaves out), and a
		// field named "elements" or "action" cannot shadow the lookup.
		const controls = Array.from(doc.querySelectorAll("input, select, textarea, button")).filter(
			(el) => (el as HTMLInputElement).form === form,
		);
		const submitControl = (el: Element) =>
			(el.tagName.toLowerCase() === "button" && ((el as HTMLButtonElement).type || "submit").toLowerCase() === "submit") ||
			(el.tagName.toLowerCase() === "input" && ["submit", "image"].includes((el as HTMLInputElement).type));
		// The control that submits: the clicked one, else the form's default
		// button (what Enter in a field uses). Its formaction / formmethod
		// override the form's own.
		const submitter = isSubmitControl(target) ? target : controls.find(submitControl) ?? null;
		const override = (attribute: "formaction" | "formmethod") =>
			submitter?.hasAttribute(attribute) ? submitter.getAttribute(attribute) : null;
		const actionOverride = override("formaction");
		const resolve = (raw: string) => {
			try {
				return new URL(raw, doc.baseURI).href;
			} catch {
				return raw;
			}
		};
		// Read the form's own attributes: form.action can be shadowed by a field.
		const formAction = Element.prototype.getAttribute.call(form, "action");
		facts.formAction = clean(resolve(actionOverride ?? formAction ?? doc.baseURI), 500);
		facts.formMethod = (override("formmethod") ?? Element.prototype.getAttribute.call(form, "method") ?? "get").toLowerCase();
		facts.formHasPayment = controls.some((el) => ["input", "select"].includes(el.tagName.toLowerCase()) && paymentField(el));
		const passwords = controls.filter((el) => el.tagName.toLowerCase() === "input" && (el as HTMLInputElement).type === "password");
		facts.formHasPassword = passwords.length > 0;
		// A sign-in has one current password; a change or reset asks for a new
		// one (and usually its confirmation).
		facts.formHasNewPassword =
			passwords.length > 1 || passwords.some((el) => /\bnew-password\b/i.test(el.getAttribute("autocomplete") ?? ""));
		facts.submitName = submitter ? accessibleName(submitter) : undefined;
		// Enter in a single-line field submits its form (implicit submission);
		// in a textarea it inserts a newline.
		const textField = tag === "input" && !["checkbox", "radio", "submit", "button", "reset", "image", "file"].includes((target as HTMLInputElement).type);
		facts.isSubmit = isSubmitControl(target) || (request.mode === "focused" && textField);
	}
	const rect = target.getBoundingClientRect();
	if (rect.width > 0 && rect.height > 0) {
		facts.rect = { x: rect.x, y: rect.y, width: rect.width, height: rect.height };
	}
	return facts;
}

/**
 * Source text that evaluates describeApprovalTarget in the page and returns
 * `{ facts, viewport }`.
 */
export function approvalDescribeScript(request: ApprovalDescribeRequest): string {
	// For Enter, the focused element gets an id kept in this (isolated) world,
	// so APPROVAL_FOCUS_SCRIPT can confirm at dispatch that focus did not move.
	const markFocus = request.mode === "focused";
	return `(() => {
	const facts = (${describeApprovalTarget.toString()})(document, ${JSON.stringify(request)});
	let focus;
	const el = document.activeElement;
	if (${markFocus} && el && el !== document.body) {
		const marks = (window.__aoApprovalFocus = window.__aoApprovalFocus || new WeakMap());
		focus = marks.get(el);
		if (!focus) {
			focus = typeof crypto !== "undefined" && crypto.randomUUID ? crypto.randomUUID() : String(Math.random()).slice(2);
			marks.set(el, focus);
		}
	}
	return { facts, viewport: { width: window.innerWidth, height: window.innerHeight }, ...(focus ? { focus } : {}) };
})()`;
}

/** Source text returning the focus id approvalDescribeScript gave the focused element, or "". */
export const APPROVAL_FOCUS_SCRIPT = `(() => {
	const marks = window.__aoApprovalFocus;
	const el = document.activeElement;
	return (marks && el && marks.get(el)) || "";
})()`;

/** A crop rectangle around the element, padded and kept inside the viewport. */
export function approvalCropRect(
	rect: { x: number; y: number; width: number; height: number } | undefined,
	viewport: { width: number; height: number },
): { x: number; y: number; width: number; height: number } | undefined {
	if (!rect || viewport.width <= 0 || viewport.height <= 0) return undefined;
	const padX = Math.max(160, rect.width);
	const padY = Math.max(90, rect.height * 2);
	const x = Math.max(0, Math.floor(rect.x - padX));
	const y = Math.max(0, Math.floor(rect.y - padY));
	const right = Math.min(viewport.width, Math.ceil(rect.x + rect.width + padX));
	const bottom = Math.min(viewport.height, Math.ceil(rect.y + rect.height + padY));
	if (right <= x || bottom <= y) return undefined;
	return { x, y, width: right - x, height: bottom - y };
}
