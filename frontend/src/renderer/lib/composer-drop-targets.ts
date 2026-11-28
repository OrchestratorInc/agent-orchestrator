/**
 * Chat composers that can take files dropped anywhere in the window, not only
 * on the composer itself. The shell's window-level drop listener routes a file
 * drop that nothing else claimed (a terminal, a PR comment box) to the
 * composer nearest the drop point.
 */
export interface ComposerDropTarget {
	element: () => HTMLElement | null;
	/** Whether the composer can take files right now (no send or staging in flight). */
	accepts: () => boolean;
	/** Attaches the files; false when the composer cannot take them right now. */
	attach: (files: File[]) => boolean;
	setDragging: (dragging: boolean) => void;
}

const targets = new Set<ComposerDropTarget>();
const lastFocused = new WeakMap<ComposerDropTarget, number>();
let focusCounter = 0;

export function registerComposerDropTarget(target: ComposerDropTarget): () => void {
	targets.add(target);
	return () => {
		targets.delete(target);
	};
}

export function noteComposerDropTargetFocus(target: ComposerDropTarget): void {
	lastFocused.set(target, ++focusCounter);
}

/**
 * The composer that owns a drop on `node`: walking up from the drop point, the
 * first ancestor holding a visible composer decides, so a drop in a split pane
 * or a side chat goes to that pane's composer. Ties go to the composer the user
 * last focused.
 */
export function composerDropTargetFor(node: EventTarget | null): ComposerDropTarget | undefined {
	const visible: Array<{ target: ComposerDropTarget; element: HTMLElement }> = [];
	for (const target of targets) {
		const element = target.element();
		if (element && isShown(element)) visible.push({ target, element });
	}
	if (visible.length === 0) return undefined;
	let ancestor: Node | null = node instanceof Node ? node : null;
	while (ancestor) {
		const scope = ancestor;
		const inside = visible.filter(({ element }) => scope.contains(element));
		if (inside.length > 0) return mostRecentlyFocused(inside.map(({ target }) => target));
		ancestor = ancestor.parentNode;
	}
	return mostRecentlyFocused(visible.map(({ target }) => target));
}

// Hidden panes stay laid out (`invisible` plus `inert`), and a modal dialog
// marks the app behind it aria-hidden, so layout alone does not mean shown.
function isShown(element: HTMLElement): boolean {
	return element.isConnected && element.getClientRects().length > 0 && !element.closest('[inert], [aria-hidden="true"]');
}

function mostRecentlyFocused(candidates: ComposerDropTarget[]): ComposerDropTarget {
	return candidates.reduce((best, candidate) =>
		(lastFocused.get(candidate) ?? 0) > (lastFocused.get(best) ?? 0) ? candidate : best);
}
