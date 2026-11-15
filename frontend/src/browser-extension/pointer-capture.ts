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
			window.addEventListener("pointermove", observe, true);
		}
		world.aoMouseCapture.pointerId = undefined;
		return true;
	}
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
