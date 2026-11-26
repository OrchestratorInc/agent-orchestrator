/**
 * What the New task button beside the tab bar does.
 *
 * The button belongs to the tab bar, not to a page, but a page knows how a task
 * it starts should begin: the board starts one on the machine and project it is
 * filtered to. The focused page registers that here, and the button falls back
 * to a plain new task elsewhere.
 */
let handler: (() => void) | undefined;

/** Registers the focused page's way to start a task; the returned call withdraws it. */
export function setNewTaskHandler(next: () => void): () => void {
	handler = next;
	return () => {
		if (handler === next) handler = undefined;
	};
}

export function startNewTask(fallback: () => void): void {
	(handler ?? fallback)();
}
