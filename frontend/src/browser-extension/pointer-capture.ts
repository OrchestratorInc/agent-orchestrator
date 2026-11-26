/**
 * Runs in each frame's isolated extension world. Track the actual mouse pointer
 * ID (never assume it is 1), then release capture before cancelled input's up.
 * Kept self-contained because chrome.scripting serializes this function.
 */
export function mouseCapture(action: "track" | "observed" | "release", pointerIds: number[] = []): boolean | "idle" | number {
	const world = globalThis as unknown as {
		aoMouseCapture?: { pointerId?: number };
		chrome?: { dom?: { openOrClosedShadowRoot(element: Element): ShadowRoot | null } };
	};
	if (action === "track") {
		if (!world.aoMouseCapture) {
			const state: { pointerId?: number } = {};
			world.aoMouseCapture = state;
			const observe = (event: PointerEvent) => {
				if (event.isTrusted && event.pointerType === "mouse" && event.buttons !== 0) state.pointerId = event.pointerId;
			};
			window.addEventListener("pointerdown", observe, true);
			// A held drag may enter a different frame, which can then capture it.
			window.addEventListener("pointermove", observe, true);
		}
		world.aoMouseCapture.pointerId = undefined;
		return true;
	}
	// A new document/frame that did not see preparation cannot prove that its
	// pointer is uncaptured. Fail closed rather than sending an activating up.
	if (!world.aoMouseCapture) return false;
	const id = world.aoMouseCapture.pointerId;
	if (action === "observed") return id ?? "idle";
	const ids = [...new Set([...pointerIds, ...(id === undefined ? [] : [id])])];
	if (ids.length === 0) return "idle";
	const visit = (root: Document | ShadowRoot) => {
		for (const element of root.querySelectorAll("*")) {
			for (const pointerId of ids) {
				if (element.hasPointerCapture(pointerId)) element.releasePointerCapture(pointerId);
			}
			const shadow = (element instanceof HTMLElement ? world.chrome?.dom?.openOrClosedShadowRoot(element) : null) ?? element.shadowRoot;
			if (shadow) visit(shadow);
		}
	};
	visit(document);
	world.aoMouseCapture.pointerId = undefined;
	return true;
}
