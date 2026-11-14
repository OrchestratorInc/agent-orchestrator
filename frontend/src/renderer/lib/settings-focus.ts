export const SETTINGS_FOCUS_HIGHLIGHT_MS = 2_000;

const FOCUS_CLASS = "settings-focus-target";

function normalize(text: string | null | undefined): string {
	return (text ?? "").replace(/\s+/g, " ").trim().toLocaleLowerCase();
}

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

export function highlightSettingsTarget(target: HTMLElement): () => void {
	target.scrollIntoView({ block: target.tagName === "SECTION" ? "start" : "center" });
	target.classList.add(FOCUS_CLASS);
	return () => target.classList.remove(FOCUS_CLASS);
}
