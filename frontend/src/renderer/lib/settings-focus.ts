// Finds and marks one setting on a settings page by its visible label, so a
// deep link (a command palette hit) lands on the row, not just the page.

/** How long the opened setting stays marked, matching the harness focus highlight. */
export const SETTINGS_FOCUS_HIGHLIGHT_MS = 2_000;

const FOCUS_CLASS = "settings-focus-target";

function normalize(text: string | null | undefined): string {
	return (text ?? "").replace(/\s+/g, " ").trim().toLocaleLowerCase();
}

/**
 * The element on a settings page that shows `label`: the row it sits in, the
 * section it heads, or the control it names. The innermost element whose text
 * is exactly the label wins; an aria-label (an icon button) is the fallback.
 * The page title is never a match.
 */
export function findSettingsTarget(root: ParentNode, label: string): HTMLElement | null {
	const wanted = normalize(label);
	if (!wanted) return null;
	const elements = Array.from(root.querySelectorAll<HTMLElement>("*")).filter((element) => element.tagName !== "H1" && !element.closest("[aria-hidden='true'], [hidden]"));
	const byText = elements.filter((element) => normalize(element.textContent) === wanted);
	const match = byText.find((element) => !byText.some((other) => other !== element && element.contains(other)))
		?? elements.find((element) => normalize(element.getAttribute("aria-label")) === wanted);
	if (!match) return null;
	const row = match.closest<HTMLElement>(".settings-row-bar, .settings-content-row");
	if (row) return row;
	if (/^H[2-6]$/.test(match.tagName)) return match.closest<HTMLElement>("section") ?? match;
	return match.closest<HTMLElement>("button, label, [role='switch']") ?? match;
}

/** Scrolls `target` into view and marks it; the returned function removes the mark. */
export function highlightSettingsTarget(target: HTMLElement): () => void {
	target.scrollIntoView({ block: target.tagName === "SECTION" ? "start" : "center" });
	target.classList.add(FOCUS_CLASS);
	return () => target.classList.remove(FOCUS_CLASS);
}
