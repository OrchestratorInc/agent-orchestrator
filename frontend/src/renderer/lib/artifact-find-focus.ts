// Contextual find belongs to the last pane used, even when clicking text did
// not move keyboard focus. Capture once per document; readers register roots.
const roots = new Set<HTMLElement>();
let lastPane: Element | null = null;
const nativeFind = ".cm-editor, .monaco-editor, .xterm, .terminal-surface, [data-editing='true']";
function visible(element: HTMLElement): boolean {
	return element.isConnected && element.getClientRects().length > 0 && !element.closest("[hidden], [aria-hidden='true'], [data-state='closed']");
}
function record(event: Event) {
	for (const target of event.composedPath()) {
		if (!(target instanceof Element)) continue;
		const container = target.closest("[data-artifact-find-container]");
		// Remember the card even while folded: its reader registers after unfolding.
		const pane = container ?? target.closest("[data-artifact-find-scope], [data-chat-find-surface], [data-chat-find-scope]");
		if (pane) { lastPane = pane; return; }
	}
}
export function registerArtifactFindRoot(root: HTMLElement): () => void {
	if (!roots.size) {
		document.addEventListener("pointerdown", record, true);
		document.addEventListener("focusin", record, true);
	}
	roots.add(root);
	return () => {
		roots.delete(root);
		if (lastPane === root) lastPane = null;
		if (!roots.size) {
			document.removeEventListener("pointerdown", record, true);
			document.removeEventListener("focusin", record, true);
		}
	};
}
export function claimArtifactFind(root: HTMLElement): void { lastPane = root; }
export function artifactFindOwner(): HTMLElement | undefined {
	const focused = document.activeElement;
	if (focused instanceof Element && focused.closest(nativeFind)) return;
	const dialogs = Array.from(document.querySelectorAll<HTMLElement>("[role='dialog'], [role='alertdialog'], [aria-modal='true']")).filter(visible);
	const dialog = dialogs.at(-1);
	const available = Array.from(roots).filter((root) => visible(root) && (!dialog || dialog.contains(root)));
	if (dialog) return available.find((root) => root.contains(focused)) ?? available.at(-1);
	if (focused instanceof HTMLIFrameElement) return available.find((root) => root.contains(focused));
	if (lastPane?.isConnected) return available.find((root) => root === lastPane || root.closest("[data-artifact-find-container]") === lastPane);
	return available.find((root) => root.contains(focused));
}
export function artifactOwnsFind(): boolean { return artifactFindOwner() !== undefined; }
