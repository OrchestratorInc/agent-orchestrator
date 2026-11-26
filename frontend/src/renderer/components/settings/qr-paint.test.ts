import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { encodeQr } from "../../lib/qr-matrix";
import { paintQr, QR_INK, QR_PAPER, quietZonePx } from "./qr-paint";

// jsdom has no canvas, so record what the painter asks of one: the shapes it
// adds to each path and the order of its fills. What matters is that the right
// modules are drawn in the right places, since a shifted or transposed grid
// would still look like a QR code and simply never scan.
interface Rect {
	x: number;
	y: number;
	w: number;
	h: number;
	radii: unknown;
}

class FakePath2D {
	rects: Rect[] = [];
	arcs: number[][] = [];
	roundRect(x: number, y: number, w: number, h: number, radii: unknown) {
		this.rects.push({ x, y, w, h, radii });
	}
	rect(x: number, y: number, w: number, h: number) {
		this.rects.push({ x, y, w, h, radii: 0 });
	}
	arc(...args: number[]) {
		this.arcs.push(args);
	}
}

function fakeContext() {
	const events: Array<{ type: string; style?: unknown; path?: FakePath2D; rule?: string; args?: number[] }> = [];
	let fillStyle: unknown = "";
	const ctx = {
		get fillStyle() {
			return fillStyle;
		},
		set fillStyle(value: unknown) {
			fillStyle = value;
		},
		save: () => undefined,
		restore: () => undefined,
		fillRect: (...args: number[]) => events.push({ type: "fillRect", style: fillStyle, args }),
		fill: (path: FakePath2D, rule?: string) => events.push({ type: "fill", style: fillStyle, path, rule }),
		drawImage: (...args: unknown[]) => events.push({ type: "drawImage", args: args.slice(1) as number[] }),
	};
	return { ctx: ctx as unknown as CanvasRenderingContext2D, events };
}

const SIZE = 240;
const PAYLOAD = "aomobile://pair#abc";

beforeEach(() => {
	vi.stubGlobal("Path2D", FakePath2D);
});

afterEach(() => {
	vi.unstubAllGlobals();
});

function paint(options: { showLogo: boolean; logo?: HTMLImageElement | null }, payload = PAYLOAD) {
	const matrix = encodeQr(payload, { version: 17 });
	const margin = quietZonePx(SIZE, matrix.modules);
	const { ctx, events } = fakeContext();
	paintQr(ctx, matrix, { size: SIZE, margin, ...options });
	return { matrix, margin, events, cell: (SIZE - 2 * margin) / matrix.modules };
}

describe("paintQr", () => {
	// Android's scanner cannot read an inverted code: ink on an opaque light card.
	it("draws dark modules on an opaque light card", () => {
		const { events } = paint({ showLogo: false });
		expect(events[0]).toMatchObject({ type: "fillRect", style: QR_PAPER, args: [0, 0, SIZE, SIZE] });
		const fills = events.filter((event) => event.type === "fill");
		expect(fills.length).toBeGreaterThan(0);
		for (const fill of fills) expect(fill.style).toBe(QR_INK);
	});

	it("draws every ordinary dark module, once, at its own grid position", () => {
		const { matrix, margin, cell, events } = paint({ showLogo: false });
		const dots = events.find((event) => event.type === "fill" && event.rule === undefined)!.path!;
		const drawn = new Set(dots.rects.map((rect) => `${Math.round((rect.y - margin) / cell)},${Math.round((rect.x - margin) / cell)}`));

		const far = matrix.modules - 7;
		const expected = new Set<string>();
		for (let row = 0; row < matrix.modules; row++) {
			for (let col = 0; col < matrix.modules; col++) {
				const inFinder = (row < 7 && col < 7) || (row < 7 && col >= far) || (row >= far && col < 7);
				if (!inFinder && matrix.isDark(row, col)) expected.add(`${row},${col}`);
			}
		}
		expect(dots.rects).toHaveLength(expected.size);
		expect(drawn).toEqual(expected);
		for (const rect of dots.rects) {
			expect(rect.w).toBeCloseTo(cell);
			expect(rect.h).toBeCloseTo(cell);
		}
	});

	it("draws the three finder squares as a ring around a dot", () => {
		const { matrix, margin, cell, events } = paint({ showLogo: false });
		const rings = events.filter((event) => event.type === "fill" && event.rule === "evenodd");
		expect(rings).toHaveLength(3);
		const far = matrix.modules - 7;
		const origins = rings.map((ring) => [Math.round((ring.path!.rects[0].y - margin) / cell), Math.round((ring.path!.rects[0].x - margin) / cell)]);
		expect(origins).toEqual(expect.arrayContaining([[0, 0], [0, far], [far, 0]]));
		for (const ring of rings) {
			// An outer square of 7 modules with a 5-module hole.
			expect(ring.path!.rects[0].w).toBeCloseTo(7 * cell);
			expect(ring.path!.rects[1].w).toBeCloseTo(5 * cell);
		}
	});

	// Hidden modules are errors the scanner must repair, so the logo's cut-out
	// is a budget: 45% of what error correction M can recover.
	it("keeps the logo cut-out within the error-correction budget and centred", () => {
		const { matrix, margin, cell, events } = paint({ showLogo: true });
		const withLogo = events.find((event) => event.type === "fill" && event.rule === undefined)!.path!;
		const without = paint({ showLogo: false }).events.find((event) => event.type === "fill" && event.rule === undefined)!.path!;
		const hiddenModules = without.rects.length - withLogo.rects.length;
		expect(hiddenModules).toBeGreaterThan(0);
		expect(hiddenModules).toBeLessThanOrEqual(Math.floor(0.45 * 0.15 * matrix.modules * matrix.modules));

		// Centred: the dots missing from the full code form a square around the middle.
		const kept = new Set(withLogo.rects.map((rect) => `${Math.round((rect.y - margin) / cell)},${Math.round((rect.x - margin) / cell)}`));
		const missing = without.rects
			.map((rect) => [Math.round((rect.y - margin) / cell), Math.round((rect.x - margin) / cell)] as const)
			.filter(([row, col]) => !kept.has(`${row},${col}`));
		const rows = missing.map(([row]) => row);
		const cols = missing.map(([, col]) => col);
		const middle = (matrix.modules - 1) / 2;
		expect((Math.min(...rows) + Math.max(...rows)) / 2).toBeCloseTo(middle, 0);
		expect((Math.min(...cols) + Math.max(...cols)) / 2).toBeCloseTo(middle, 0);
	});

	it("draws the logo in the middle, inside the cut-out, once it is loaded", () => {
		const logo = { naturalWidth: 100, naturalHeight: 100 } as HTMLImageElement;
		const { margin, cell, events, matrix } = paint({ showLogo: true, logo });
		const drawImage = events.find((event) => event.type === "drawImage")!;
		const [x, y, w, h] = drawImage.args!;
		const centre = margin + (matrix.modules / 2) * cell;
		expect(x + w / 2).toBeCloseTo(centre);
		expect(y + h / 2).toBeCloseTo(centre);
		expect(w).toBeGreaterThan(0);
		// Well inside the code, never reaching a finder square.
		expect(w).toBeLessThan((matrix.modules - 14) * cell);
	});

	it("draws no logo while it is still loading, and none when not asked", () => {
		expect(paint({ showLogo: true, logo: null }).events.some((event) => event.type === "drawImage")).toBe(false);
		const logo = { naturalWidth: 100, naturalHeight: 100 } as HTMLImageElement;
		expect(paint({ showLogo: false, logo }).events.some((event) => event.type === "drawImage")).toBe(false);
	});

	it("keeps the quiet zone the spec requires, in modules", () => {
		for (const modules of [29, 85]) {
			const margin = quietZonePx(SIZE, modules);
			const cell = (SIZE - 2 * margin) / modules;
			expect(margin / cell).toBeGreaterThanOrEqual(4);
			expect(margin / cell).toBeLessThan(4.5);
		}
	});
});
