/**
 * App-wide overlay scrollbars.
 *
 * Chromium on macOS gives a styled scrollbar its own layout width, and
 * `overflow: overlay` is gone, so styles.css hides every native bar and this
 * module draws a thin thumb on top of whichever element is being scrolled or
 * hovered. Thumbs live in one fixed layer, are positioned with transforms at
 * most once per frame, and never touch React or the scroller's layout.
 */

const THUMB_SIZE = 4;
const THUMB_INSET = 2;
const MIN_THUMB = 24;
const IDLE_MS = 400;
/** Matches the thumb's opacity transition in styles.css. */
const FADE_MS = 150;
/** A scroll this soon after wheel, touch, key, or pointer input is the person's. */
const INPUT_WINDOW_MS = 1000;

type Axis = "x" | "y";

type Bar = {
	el: HTMLElement;
	thumbs: Record<Axis, HTMLDivElement>;
	hovered: boolean;
	dragging: boolean;
	hideTimer?: ReturnType<typeof setTimeout>;
};

/** Elements that draw their own scrollbars or must keep the native one. */
function optedOut(el: Element): boolean {
	return el.closest(".xterm, .repository-picker-scrollbar, [data-overlay-scrollbar='off']") !== null;
}

/** Tab strips hide their bar on purpose: a thumb there would sit on the tabs. */
function hidesAxis(el: HTMLElement, axis: Axis): boolean {
	return axis === "x" && el.classList.contains("scrollbar-none");
}

function scrollsOn(el: HTMLElement, axis: Axis, style: CSSStyleDeclaration): boolean {
	const overflow = axis === "y" ? style.overflowY : style.overflowX;
	if (overflow !== "auto" && overflow !== "scroll") return false;
	return axis === "y" ? el.scrollHeight > el.clientHeight + 1 : el.scrollWidth > el.clientWidth + 1;
}

function isScroller(el: HTMLElement): boolean {
	const style = getComputedStyle(el);
	return scrollsOn(el, "y", style) || scrollsOn(el, "x", style);
}

export function installOverlayScrollbars(doc: Document = document): () => void {
	const view = doc.defaultView ?? window;
	const layer = doc.createElement("div");
	layer.className = "overlay-scrollbar-layer";
	layer.setAttribute("aria-hidden", "true");
	doc.body.append(layer);

	const bars = new Map<HTMLElement, Bar>();
	const dirty = new Set<Bar>();
	let frame = 0;
	let hoveredBar: Bar | null = null;

	let lastInput: { at: number; target: EventTarget | null } = { at: Number.NEGATIVE_INFINITY, target: null };

	const remove = (bar: Bar) => {
		clearTimeout(bar.hideTimer);
		bar.thumbs.x.remove();
		bar.thumbs.y.remove();
		bars.delete(bar.el);
		dirty.delete(bar);
		if (hoveredBar === bar) hoveredBar = null;
	};

	const position = (bar: Bar) => {
		const el = bar.el;
		if (!el.isConnected) {
			remove(bar);
			return;
		}
		const rect = el.getBoundingClientRect();
		const style = getComputedStyle(el);
		const left = rect.left + el.clientLeft;
		const top = rect.top + el.clientTop;
		const { clientHeight, clientWidth, scrollHeight, scrollLeft, scrollTop, scrollWidth } = el;

		const y = bar.thumbs.y;
		if (scrollsOn(el, "y", style)) {
			const height = Math.max(MIN_THUMB, (clientHeight * clientHeight) / scrollHeight);
			const offset = (scrollTop / (scrollHeight - clientHeight)) * (clientHeight - height);
			y.style.display = "block";
			y.style.height = `${height}px`;
			y.style.transform = `translate(${left + clientWidth - THUMB_SIZE - THUMB_INSET}px, ${top + offset}px)`;
		} else {
			y.style.display = "none";
		}

		const x = bar.thumbs.x;
		if (scrollsOn(el, "x", style) && !hidesAxis(el, "x")) {
			const width = Math.max(MIN_THUMB, (clientWidth * clientWidth) / scrollWidth);
			const offset = (Math.abs(scrollLeft) / (scrollWidth - clientWidth)) * (clientWidth - width);
			x.style.display = "block";
			x.style.width = `${width}px`;
			x.style.transform = `translate(${left + offset}px, ${top + clientHeight - THUMB_SIZE - THUMB_INSET}px)`;
		} else {
			x.style.display = "none";
		}
	};

	const flush = () => {
		frame = 0;
		for (const bar of dirty) position(bar);
		dirty.clear();
	};

	const schedule = (bar: Bar) => {
		dirty.add(bar);
		if (!frame) frame = view.requestAnimationFrame(flush);
	};

	const setVisible = (bar: Bar, visible: boolean) => {
		for (const thumb of [bar.thumbs.x, bar.thumbs.y]) {
			if (visible) thumb.dataset.visible = "";
			else delete thumb.dataset.visible;
		}
	};

	/**
	 * Show now, and fade once the bar is idle, unhovered, and not dragged. A
	 * faded bar is dropped, so the map never holds elements React removed.
	 */
	const reveal = (bar: Bar) => {
		setVisible(bar, true);
		clearTimeout(bar.hideTimer);
		bar.hideTimer = setTimeout(() => {
			if (bar.hovered || bar.dragging) return;
			setVisible(bar, false);
			bar.hideTimer = setTimeout(() => remove(bar), FADE_MS);
		}, IDLE_MS);
	};

	const makeThumb = (bar: () => Bar, axis: Axis) => {
		const thumb = doc.createElement("div");
		thumb.className = "overlay-scrollbar-thumb";
		thumb.dataset.axis = axis;
		thumb.style.display = "none";
		let start = { pointer: 0, scroll: 0 };
		thumb.addEventListener("pointerdown", (event) => {
			const { el } = bar();
			event.preventDefault();
			thumb.setPointerCapture(event.pointerId);
			start = axis === "y" ? { pointer: event.clientY, scroll: el.scrollTop } : { pointer: event.clientX, scroll: el.scrollLeft };
			bar().dragging = true;
			thumb.dataset.dragging = "";
		});
		thumb.addEventListener("pointermove", (event) => {
			const current = bar();
			if (!current.dragging) return;
			const el = current.el;
			const track = axis === "y" ? el.clientHeight - thumb.offsetHeight : el.clientWidth - thumb.offsetWidth;
			if (track <= 0) return;
			const range = axis === "y" ? el.scrollHeight - el.clientHeight : el.scrollWidth - el.clientWidth;
			const delta = ((axis === "y" ? event.clientY : event.clientX) - start.pointer) * (range / track);
			if (axis === "y") el.scrollTop = start.scroll + delta;
			else el.scrollLeft = start.scroll + delta;
		});
		const endDrag = () => {
			const current = bar();
			current.dragging = false;
			delete thumb.dataset.dragging;
			reveal(current);
		};
		thumb.addEventListener("pointerup", endDrag);
		thumb.addEventListener("lostpointercapture", endDrag);
		thumb.addEventListener("pointerenter", () => {
			bar().hovered = true;
			reveal(bar());
		});
		thumb.addEventListener("pointerleave", () => {
			const current = bar();
			current.hovered = current === hoveredBar;
			reveal(current);
		});
		layer.append(thumb);
		return thumb;
	};

	const barFor = (el: HTMLElement): Bar => {
		const existing = bars.get(el);
		if (existing) return existing;
		let created: Bar;
		const get = () => created;
		created = { el, thumbs: { x: makeThumb(get, "x"), y: makeThumb(get, "y") }, hovered: false, dragging: false };
		bars.set(el, created);
		return created;
	};

	const onInput = (event: Event) => {
		lastInput = { at: view.performance.now(), target: event.target };
	};

	/** Whether the person's own recent input, inside this element, scrolled it. */
	const userScrolled = (el: HTMLElement): boolean => {
		if (view.performance.now() - lastInput.at >= INPUT_WINDOW_MS) return false;
		const source = lastInput.target;
		return source instanceof Node && (el.contains(source) || source === doc.body || source === doc.documentElement);
	};

	const onScroll = (event: Event) => {
		const target = event.target === doc ? doc.scrollingElement : event.target;
		if (!(target instanceof HTMLElement) || optedOut(target)) return;
		// A scroll the app made (e.g. following streamed chat) only moves a thumb
		// already showing; it never reveals one, which could sit above a dialog.
		const userScroll = userScrolled(target);
		const bar = bars.get(target) ?? (userScroll ? barFor(target) : null);
		if (!bar) return;
		schedule(bar);
		if (userScroll || bar.hovered || bar.dragging) reveal(bar);
	};

	const onPointerOver = (event: PointerEvent) => {
		if (event.pointerType !== "mouse") return;
		let node = event.target instanceof Element ? event.target : null;
		if (node?.closest(".overlay-scrollbar-layer")) return;
		let scroller: HTMLElement | null = null;
		while (node && node !== doc.documentElement) {
			if (node instanceof HTMLElement && isScroller(node)) {
				scroller = node;
				break;
			}
			node = node.parentElement;
		}
		if (scroller && optedOut(scroller)) scroller = null;
		const next = scroller ? barFor(scroller) : null;
		if (next === hoveredBar) return;
		if (hoveredBar) {
			hoveredBar.hovered = false;
			reveal(hoveredBar);
		}
		hoveredBar = next;
		if (next) {
			next.hovered = true;
			schedule(next);
			reveal(next);
		}
	};

	const onResize = () => {
		for (const bar of bars.values()) schedule(bar);
	};

	const inputEvents = ["wheel", "touchmove", "keydown", "pointerdown"] as const;
	for (const type of inputEvents) doc.addEventListener(type, onInput, { capture: true, passive: true });
	doc.addEventListener("scroll", onScroll, { capture: true, passive: true });
	doc.addEventListener("pointerover", onPointerOver, { passive: true });
	view.addEventListener("resize", onResize);

	return () => {
		for (const type of inputEvents) doc.removeEventListener(type, onInput, { capture: true });
		doc.removeEventListener("scroll", onScroll, { capture: true });
		doc.removeEventListener("pointerover", onPointerOver);
		view.removeEventListener("resize", onResize);
		if (frame) view.cancelAnimationFrame(frame);
		for (const bar of [...bars.values()]) remove(bar);
		layer.remove();
	};
}
