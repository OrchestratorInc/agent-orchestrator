import type { QrMatrix } from "../../lib/qr-matrix";

export const QR_INK = "#111113";
export const QR_PAPER = "#ffffff";

const QUIET_ZONE_MODULES = 4;

export function quietZonePx(size: number, modules: number): number {
	return Math.ceil((QUIET_ZONE_MODULES * size) / (modules + 2 * QUIET_ZONE_MODULES));
}

const FINDER_MODULES = 7;

const LOGO_COVERAGE = 0.45 * 0.15;

function inFinder(row: number, col: number, modules: number): boolean {
	const far = modules - FINDER_MODULES;
	return (row < FINDER_MODULES && col < FINDER_MODULES)
		|| (row < FINDER_MODULES && col >= far)
		|| (row >= far && col < FINDER_MODULES);
}

function logoArea(modules: number): { start: number; side: number } {
	let side = Math.floor(Math.sqrt(LOGO_COVERAGE * modules * modules));
	side = Math.min(side, modules - 2 * FINDER_MODULES - 2);
	if ((modules - side) % 2 !== 0) side -= 1;
	side = Math.max(side, 0);
	return { start: (modules - side) / 2, side };
}

export interface QrPaintOptions {
	size: number;
	margin: number;
	showLogo: boolean;
	logo?: HTMLImageElement | null;
}

export function paintQr(ctx: CanvasRenderingContext2D, matrix: QrMatrix, { size, margin, showLogo, logo }: QrPaintOptions): void {
	const { modules } = matrix;
	const cell = (size - 2 * margin) / modules;
	const hidden = showLogo ? logoArea(modules) : { start: 0, side: 0 };

	const hides = (row: number, col: number) =>
		hidden.side > 0
		&& row >= hidden.start && row < hidden.start + hidden.side
		&& col >= hidden.start && col < hidden.start + hidden.side;

	const dots = new Uint8Array(modules * modules);
	for (let row = 0; row < modules; row++) {
		for (let col = 0; col < modules; col++) {
			if (!inFinder(row, col, modules) && !hides(row, col) && matrix.isDark(row, col)) dots[row * modules + col] = 1;
		}
	}
	const drawn = (row: number, col: number) => row >= 0 && col >= 0 && row < modules && col < modules && dots[row * modules + col] === 1;

	ctx.save();
	ctx.fillStyle = QR_PAPER;
	ctx.fillRect(0, 0, size, size);

	ctx.fillStyle = QR_INK;
	const radius = cell / 2;
	const path = new Path2D();
	for (let row = 0; row < modules; row++) {
		for (let col = 0; col < modules; col++) {
			if (dots[row * modules + col] === 0) continue;
			const x = margin + col * cell;
			const y = margin + row * cell;
			const up = drawn(row - 1, col);
			const down = drawn(row + 1, col);
			const left = drawn(row, col - 1);
			const right = drawn(row, col + 1);
			const tl = !up && !left;
			const tr = !up && !right;
			const br = !down && !right;
			const bl = !down && !left;
			if (tl || tr || br || bl) path.roundRect(x, y, cell, cell, [tl ? radius : 0, tr ? radius : 0, br ? radius : 0, bl ? radius : 0]);
			else path.rect(x, y, cell, cell);
		}
	}
	ctx.fill(path);

	const far = modules - FINDER_MODULES;
	for (const [row, col] of [[0, 0], [0, far], [far, 0]]) {
		const x = margin + col * cell;
		const y = margin + row * cell;
		const ring = new Path2D();
		ring.roundRect(x, y, FINDER_MODULES * cell, FINDER_MODULES * cell, 2.2 * cell);
		ring.roundRect(x + cell, y + cell, (FINDER_MODULES - 2) * cell, (FINDER_MODULES - 2) * cell, 1.2 * cell);
		ctx.fill(ring, "evenodd");
		const centre = new Path2D();
		centre.arc(x + (FINDER_MODULES / 2) * cell, y + (FINDER_MODULES / 2) * cell, 1.5 * cell, 0, Math.PI * 2);
		ctx.fill(centre);
	}

	if (logo && hidden.side > 2) {
		const box = (hidden.side - 2) * cell;
		const aspect = logo.naturalWidth > 0 && logo.naturalHeight > 0 ? logo.naturalWidth / logo.naturalHeight : 1;
		const width = aspect >= 1 ? box : box * aspect;
		const height = aspect >= 1 ? box / aspect : box;
		const centre = margin + (modules / 2) * cell;
		ctx.drawImage(logo, centre - width / 2, centre - height / 2, width, height);
	}
	ctx.restore();
}
