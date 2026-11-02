import { act, render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { PairingQr, QR_FADE_MS } from "./PairingQr";

// jsdom has no canvas, so stand in a code that exposes the value it was given:
// the crossfade is what is under test, not the drawing.
vi.mock("./QrCanvas", () => ({
	PAIRING_QR_VERSION: 17,
	QrCanvas: ({ value, ...rest }: { value: string | null; "data-qr-value"?: string }) => (
		<canvas data-code={value} data-qr-value={rest["data-qr-value"]} />
	),
}));

const A = "aomobile://pair#a";
const B = "aomobile://pair#b";
const C = "aomobile://pair#c";

/** The slot (opacity wrapper) holding `code`, or null once it is gone. */
function slotFor(container: HTMLElement, code: string) {
	return container.querySelector(`[data-code="${code}"]`)?.parentElement ?? null;
}

/** Two animation frames: what it takes for a hidden code to start fading in. */
async function flushFrames() {
	await act(async () => {
		vi.advanceTimersByTime(40);
	});
}

async function advance(ms: number) {
	await act(async () => {
		vi.advanceTimersByTime(ms);
	});
}

const RETIRED = QR_FADE_MS + 60;

function mockReducedMotion(reduced: boolean) {
	vi.stubGlobal("matchMedia", (query: string) => ({ matches: reduced && query.includes("reduce"), addEventListener() {}, removeEventListener() {} }));
}

beforeEach(() => {
	vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "requestAnimationFrame", "cancelAnimationFrame"] });
	mockReducedMotion(false);
});

afterEach(() => {
	vi.useRealTimers();
	vi.unstubAllGlobals();
});

describe("PairingQr", () => {
	// A code withheld because the connector is not advertisable must not be
	// scannable: one scanned early carries no tunnel endpoint and would pair
	// here then fail from every other network.
	it("exposes no scannable code before one exists", () => {
		const { container } = render(<PairingQr value={null} size={240} />);
		expect(container.querySelector("canvas")).toBeNull();
		expect(container.querySelector("[data-qr-value]")).toBeNull();
	});

	// Opening the page (or coming back to it) is not a change: a code that
	// already exists is on screen in the first frame, with no fade.
	it("shows a code that exists at mount at once, without a transition", () => {
		const { container, unmount } = render(<PairingQr value={A} size={240} />);
		expect(slotFor(container, A)).toHaveClass("opacity-100", "transition-none");
		expect(container.querySelector("[data-qr-value]")).toHaveAttribute("data-qr-value", A);

		unmount();
		const again = render(<PairingQr value={A} size={240} />);
		expect(slotFor(again.container, A)).toHaveClass("opacity-100", "transition-none");
	});

	// A code that arrives or changes without the user asking (the status
	// landing, a poll picking up the tunnel) swaps in at once.
	it("swaps a change the user did not trigger without a fade", async () => {
		const { container, rerender } = render(<PairingQr value={null} size={240} />);
		rerender(<PairingQr value={A} size={240} />);
		expect(slotFor(container, A)).toHaveClass("opacity-100", "transition-none");

		rerender(<PairingQr value={B} size={240} />);
		expect(slotFor(container, A)).toBeNull();
		expect(slotFor(container, B)).toHaveClass("opacity-100", "transition-none");
		expect(container.querySelectorAll("canvas")).toHaveLength(1);
	});

	it("keeps the previous code on screen until the next one has faded in over it", async () => {
		const { container, rerender } = render(<PairingQr value={A} size={240} />);

		rerender(<PairingQr value={B} size={240} animate />);
		// Both are drawn; the new one waits hidden, and only it is the scannable value.
		expect(slotFor(container, A)).toHaveClass("opacity-100");
		expect(slotFor(container, B)).toHaveClass("opacity-0", "transition-none");
		expect(container.querySelectorAll("[data-qr-value]")).toHaveLength(1);
		expect(container.querySelector("[data-qr-value]")).toHaveAttribute("data-qr-value", B);

		// Fading in on top, the old one still fully opaque beneath: the
		// placeholder never shows through a half-faded code.
		await flushFrames();
		expect(slotFor(container, B)).toHaveClass("opacity-100", "transition-opacity");
		expect(slotFor(container, A)).toHaveClass("opacity-100");

		await advance(QR_FADE_MS);
		expect(slotFor(container, A)).not.toBeNull();

		await advance(RETIRED);
		expect(slotFor(container, A)).toBeNull();
		expect(slotFor(container, B)).toHaveClass("opacity-100", "transition-none");
	});

	it("stacks the newest code above the older one", async () => {
		const { container, rerender } = render(<PairingQr value={A} size={240} />);
		rerender(<PairingQr value={B} size={240} animate />);
		expect(slotFor(container, B)).toHaveClass("z-10");
		expect(slotFor(container, A)).toHaveClass("z-0");
	});

	// A second change while the first is still fading must not leave the first
	// half-transparent over a placeholder: it is settled, then covered in turn.
	it("settles a fade in flight when another change arrives", async () => {
		const { container, rerender } = render(<PairingQr value={A} size={240} />);
		rerender(<PairingQr value={B} size={240} animate />);
		await flushFrames();

		rerender(<PairingQr value={C} size={240} animate />);
		expect(slotFor(container, A)).toBeNull();
		expect(slotFor(container, B)).toHaveClass("opacity-100", "transition-none");
		expect(slotFor(container, C)).toHaveClass("opacity-0");

		await flushFrames();
		expect(slotFor(container, C)).toHaveClass("opacity-100", "transition-opacity");
		await advance(RETIRED);
		expect(slotFor(container, B)).toBeNull();
		expect(slotFor(container, C)).toHaveClass("opacity-100");
	});

	it("fades the code out when it is withdrawn, then clears it", async () => {
		const { container, rerender } = render(<PairingQr value={A} size={240} />);

		rerender(<PairingQr value={null} size={240} animate />);
		expect(container.querySelector("[data-qr-value]")).toBeNull();
		expect(slotFor(container, A)).toHaveClass("opacity-0", "transition-opacity");

		await advance(RETIRED);
		expect(container.querySelector("canvas")).toBeNull();
	});

	// Revoking pairing must not leave the credential drawn anywhere.
	it("clears the code at once when it is withdrawn without a fade", () => {
		const { container, rerender } = render(<PairingQr value={A} size={240} />);
		rerender(<PairingQr value={null} size={240} />);
		expect(container.querySelector("canvas")).toBeNull();
	});

	// Turning pairing off while a fresh code is still fading in must not leave
	// that code scannable once the fade would have finished.
	it("never shows a code withdrawn while it was still hidden", async () => {
		const { container, rerender } = render(<PairingQr value={A} size={240} />);
		rerender(<PairingQr value={B} size={240} animate />);
		rerender(<PairingQr value={null} size={240} animate />);

		await advance(RETIRED);
		expect(container.querySelector("[data-qr-value]")).toBeNull();
		expect(container.querySelector("canvas")).toBeNull();
	});

	it("swaps without a fade for reduced motion, even when asked to animate", () => {
		mockReducedMotion(true);
		const { container, rerender } = render(<PairingQr value={A} size={240} />);
		rerender(<PairingQr value={B} size={240} animate />);
		expect(slotFor(container, A)).toBeNull();
		expect(slotFor(container, B)).toHaveClass("opacity-100", "transition-none");
	});

	it("clears pending timers on unmount", async () => {
		const { rerender, unmount } = render(<PairingQr value={A} size={240} />);
		rerender(<PairingQr value={B} size={240} animate />);
		unmount();
		await expect(advance(RETIRED)).resolves.toBeUndefined();
	});
});
