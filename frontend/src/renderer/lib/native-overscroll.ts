/**
 * Native rubber band at a list's ends (macOS trackpad).
 *
 * The shell webContents runs with Electron's `scrollBounce`, so Chromium
 * stretches any scroller whose `overscroll-behavior` is `contain` once it
 * reaches an edge. Put this on the list's own scroller (headers outside it):
 * vertical only, and rigid under reduced motion. Every other scroller that must
 * not chain uses `overscroll-none`; native-overscroll.test.ts holds that line.
 */
export const NATIVE_BOUNCE_Y = "overscroll-y-contain overscroll-x-none motion-reduce:overscroll-y-none";

/** Scrollers that bounce: the class above, plus the settings menu surface (styles.css). */
const BOUNCING_SCROLLER = ".overscroll-y-contain, .settings-menu-surface";
/** A popped-up layer: menus, selects, popovers and dialogs. */
const LAYER = "[data-radix-popper-content-wrapper], [role='dialog'], [role='alertdialog'], [role='menu'], [role='listbox']";

/**
 * Radix menus, selects, popovers and dialogs lock page scrolling with
 * react-remove-scroll. Its document wheel listener cancels a wheel at a list's
 * edge (and every wheel over a list portaled outside a dialog), which also
 * cancels the native stretch. While a lock is on, a vertical wheel over a
 * bouncing list inside such a layer skips that listener; the list's own
 * `overscroll-behavior: contain` already keeps the page behind it still.
 *
 * Install once at startup: listeners on one node run in the order they were
 * added, so this must be on `document` before any lock adds its own.
 */
export function installScrollLockBouncePassThrough(doc: Document = document): () => void {
	const onWheel = (event: WheelEvent) => {
		if (event.ctrlKey || !doc.body.hasAttribute("data-scroll-locked")) return;
		if (Math.abs(event.deltaX) > Math.abs(event.deltaY)) return;
		const target = event.target;
		if (!(target instanceof Element)) return;
		const scroller = target.closest(BOUNCING_SCROLLER);
		if (!scroller?.closest(LAYER)) return;
		event.stopImmediatePropagation();
	};
	doc.addEventListener("wheel", onWheel, { passive: true });
	return () => doc.removeEventListener("wheel", onWheel);
}
