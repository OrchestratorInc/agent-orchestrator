import type { QrMatrix } from "../../lib/qr-matrix";

/**
 * Draws a QR matrix to a canvas: rounded dots, rounded finder squares and an
 * optional logo, always as dark modules on a light card.
 *
 * The polarity is a correctness constraint, not a style choice. This used to
 * take its colour from `--color-text-settings-title` over a transparent
 * background, which in the dark theme produced light modules on dark: an
 * inverted code. Apple's AVFoundation and Google Lens both decode inverted
 * codes; the bundled ML Kit scanner that expo-camera uses on Android does not.
 * So the pairing QR scanned everywhere *except* our own Android app.
 *
 * Confirmed by a single-variable scan sweep on a device that reproduced it:
 * with module size, logo size and finder shape each held constant, every
 * inverted variant failed and every dark-on-light variant passed. Polarity was
 * the only factor that predicted the outcome.
 *
 * Keep the background opaque. A transparent background inherits whatever sits
 * behind it, which is how the inversion got in.
 */
export const QR_INK = "#111113";
export const QR_PAPER = "#ffffff";

/**
 * Quiet zone the spec requires, in modules.
 *
 * Measured in modules rather than pixels or a fraction of the rendered size,
 * because that is the unit a scanner reads it in. A short payload produces
 * fewer, larger modules, so a fixed percentage that looks right on the long
 * pairing code would leave a small store code under a third of the clearance
 * it needs.
 */
const QUIET_ZONE_MODULES = 4;

/** Margin in px that yields QUIET_ZONE_MODULES around a code of `modules`
 *  modules drawn at `size` px, given the margin eats into that same size:
 *  margin / ((size - 2·margin) / modules) = QUIET_ZONE_MODULES. */
export function quietZonePx(size: number, modules: number): number {
	return Math.ceil((QUIET_ZONE_MODULES * size) / (modules + 2 * QUIET_ZONE_MODULES));
}

/** A finder square is 7 modules across, in three corners. */
const FINDER_MODULES = 7;

/**
 * The share of a code's modules the logo may hide: 45% of what error
 * correction M can recover (15%). Hidden modules are errors a scanner has to
 * repair, so this is the budget, not a taste setting.
 */
const LOGO_COVERAGE = 0.45 * 0.15;

function inFinder(row: number, col: number, modules: number): boolean {
	const far = modules - FINDER_MODULES;
	return (row < FINDER_MODULES && col < FINDER_MODULES)
		|| (row < FINDER_MODULES && col >= far)
		|| (row >= far && col < FINDER_MODULES);
}

/** The centred square of modules the logo covers, as its first module and side. */
function logoArea(modules: number): { start: number; side: number } {
	let side = Math.floor(Math.sqrt(LOGO_COVERAGE * modules * modules));
	side = Math.min(side, modules - 2 * FINDER_MODULES - 2);
	// Same parity as the grid, so the square sits exactly on its centre.
	if ((modules - side) % 2 !== 0) side -= 1;
	side = Math.max(side, 0);
	return { start: (modules - side) / 2, side };
}

export interface QrPaintOptions {
	/** Side of the square drawn, in CSS px. */
	size: number;
	/** Quiet zone in px, kept outside the modules. */
	margin: number;
	/** Reserve the middle for a logo, and draw `logo` there when it is ready. */
	showLogo: boolean;
	logo?: HTMLImageElement | null;
}

/**
 * Paint `matrix` into `ctx` in CSS px (the caller scales for the device).
 *
 * Every dot is one subpath of a single path filled once, so neighbouring dots
 * merge without seams and a 85×85 code costs one fill, not thousands of
 * elements. A dot's corner is rounded only where its two neighbours on that
 * corner are both empty, which turns runs of dots into soft, connected shapes.
 */
export function paintQr(ctx: CanvasRenderingContext2D, matrix: QrMatrix, { size, margin, showLogo, logo }: QrPaintOptions): void {
	const { modules } = matrix;
	const cell = (size - 2 * margin) / modules;
	const hidden = showLogo ? logoArea(modules) : { start: 0, side: 0 };

	const hides = (row: number, col: number) =>
		hidden.side > 0
		&& row >= hidden.start && row < hidden.start + hidden.side
		&& col >= hidden.start && col < hidden.start + hidden.side;

	// Which modules are drawn as dots: dark, and not a finder square or under the
	// logo. Read once into a flat array, so each dot's four neighbours are plain
	// lookups rather than four more passes through the encoder.
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
			// Most modules sit inside a run and have no rounded corner: a plain
			// rect is cheaper than a rounded one.
			if (tl || tr || br || bl) path.roundRect(x, y, cell, cell, [tl ? radius : 0, tr ? radius : 0, br ? radius : 0, bl ? radius : 0]);
			else path.rect(x, y, cell, cell);
		}
	}
	ctx.fill(path);

	// Finder squares: a rounded ring around a round dot.
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

	// The logo sits inside the hidden square with one module of clear paper around it.
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
