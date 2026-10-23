/**
 * Runs in each frame's isolated extension world. Track the actual mouse pointer
 * ID (never assume it is 1), then release capture before cancelled input's up.
 * Kept self-contained because chrome.scripting serializes this function.
 */
export function mouseCapture(action: "track" | "release"): boolean | "idle" {
	const world = globalThis as unknown as {
		aoMouseCapture?: { pointerId?: number };
		chrome?: { dom?: { openOrClosedShadowRoot(element: Element): ShadowRoot | null } };
	};
	if (action === "track") {
		if (!world.aoMouseCapture) {
			const state: { pointerId?: number } = {};
			world.aoMouseCapture = state;
			window.addEventListener("pointerdown", (event) => {
				if (event.isTrusted && event.pointerType === "mouse") state.pointerId = event.pointerId;
			}, true);
		}
		world.aoMouseCapture.pointerId = undefined;
		return true;
	}
	// A new document/frame that did not see preparation cannot prove that its
	// pointer is uncaptured. Fail closed rather than sending an activating up.
	if (!world.aoMouseCapture) return false;
	const id = world.aoMouseCapture.pointerId;
	if (id === undefined) return "idle";
	const visit = (root: Document | ShadowRoot) => {
		for (const element of root.querySelectorAll("*")) {
			if (element.hasPointerCapture(id)) element.releasePointerCapture(id);
			const shadow = (element instanceof HTMLElement ? world.chrome?.dom?.openOrClosedShadowRoot(element) : null) ?? element.shadowRoot;
			if (shadow) visit(shadow);
		}
	};
	visit(document);
	world.aoMouseCapture.pointerId = undefined;
	return true;
}
