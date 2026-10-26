// Bayer Vortex, adapted from Originkit: an ordered-dither spiral drawn on a small
// canvas and scaled up with nearest-neighbour sampling. Only the lit dither level
// is drawn; the rest stays transparent, so it sits on whatever surface is behind it.
import { useLayoutEffect, useRef } from "react";
import type { CSSProperties } from "react";

const BAYER8 = [
	0, 32, 8, 40, 2, 34, 10, 42, 48, 16, 56, 24, 50, 18, 58, 26, 12, 44, 4, 36,
	14, 46, 6, 38, 60, 28, 52, 20, 62, 30, 54, 22, 3, 35, 11, 43, 1, 33, 9, 41,
	51, 19, 59, 27, 49, 17, 57, 25, 15, 47, 7, 39, 13, 45, 5, 37, 63, 31, 55,
	23, 61, 29, 53, 21,
];

export interface BayerVortexProps {
	/** The lit dither level. The unlit level is transparent. */
	color: string;
	/** Dither pixel size, 1 (fine) to 20 (coarse). */
	pixel?: number;
	/** Spiral arm count, 1 to 20. */
	arms?: number;
	/** Rotation speed, 0 (still) to 20. */
	speed?: number;
	/** Where the spiral turns, as fractions of the width and height: [0.5, 0] is the middle of the top edge. */
	center?: readonly [number, number];
	style?: CSSProperties;
}

function clamp(value: number, lo: number, hi: number): number {
	return Math.max(lo, Math.min(hi, value));
}

function parseHex(hex: string): [number, number, number] {
	const h = hex.replace("#", "").trim();
	if (h.length === 3) {
		return [parseInt(h[0] + h[0], 16), parseInt(h[1] + h[1], 16), parseInt(h[2] + h[2], 16)];
	}
	if (h.length >= 6) {
		return [parseInt(h.slice(0, 2), 16), parseInt(h.slice(2, 4), 16), parseInt(h.slice(4, 6), 16)];
	}
	return [128, 128, 128];
}

/** A one-color dithered spiral turning at `speed`; a still frame when speed is 0. */
export function BayerVortex({ color, pixel = 14, arms = 12, speed = 4, center = [0.5, 0.5], style }: BayerVortexProps) {
	const containerRef = useRef<HTMLDivElement | null>(null);
	const colorRef = useRef(color);
	colorRef.current = color;

	// Layout effect: the first frame is painted before the browser paints, so the vortex never pops in.
	useLayoutEffect(() => {
		const container = containerRef.current;
		if (!container) return;
		const canvas = document.createElement("canvas");
		canvas.style.cssText = "position:absolute;inset:0;width:100%;height:100%;image-rendering:pixelated";
		container.appendChild(canvas);
		const ctx = canvas.getContext("2d");
		const buffer = document.createElement("canvas");
		const bufferCtx = buffer.getContext("2d");
		if (!ctx || !bufferCtx) {
			canvas.remove();
			return;
		}

		const cell = Math.max(1, 14 - clamp(pixel, 1, 20) * 0.55);
		const armCount = Math.round(1 + clamp(arms, 1, 20) * 0.6);
		let rate = clamp(speed, 0, 20) * 0.18;
		let width = 0;
		let height = 0;
		let image: ImageData | null = null;
		let time = 0;
		let last = performance.now();
		let frame = 0;

		const resize = () => {
			width = container.clientWidth;
			height = container.clientHeight;
			if (width <= 0 || height <= 0) return;
			canvas.width = Math.round(width);
			canvas.height = Math.round(height);
			const w = Math.max(16, Math.round(width / cell));
			const h = Math.max(16, Math.round(height / cell));
			if (w === buffer.width && h === buffer.height && image) return;
			buffer.width = w;
			buffer.height = h;
			image = bufferCtx.createImageData(w, h);
		};

		const draw = () => {
			try {
				paint();
			} catch {
				// No usable 2d canvas: stop animating and leave the surface plain.
				cancelAnimationFrame(frame);
				rate = 0;
			}
		};

		const paint = () => {
			if (!image || width <= 0) return;
			const lit = parseHex(colorRef.current);
			const bw = buffer.width;
			const bh = buffer.height;
			const cx = bw * center[0];
			const cy = bh * center[1];
			const scale = 2 / Math.min(bw, bh);
			const data = image.data;
			for (let y = 0; y < bh; y++) {
				for (let x = 0; x < bw; x++) {
					const dx = (x - cx) * scale;
					const dy = (y - cy) * scale;
					const r = Math.sqrt(dx * dx + dy * dy) + 0.0001;
					const spiral = Math.atan2(dy, dx) * armCount - time * rate * 4;
					let v = 0.5 + 0.5 * Math.sin(spiral);
					v *= Math.min(1, r * 3.2) * Math.max(0, 1 - r * 0.55);
					const threshold = BAYER8[(y & 7) * 8 + (x & 7)] / 64 - 0.5;
					const f = Math.round(v + threshold) > 0 ? 1 : 0;
					const i = (y * bw + x) * 4;
					data[i] = lit[0];
					data[i + 1] = lit[1];
					data[i + 2] = lit[2];
					data[i + 3] = f ? 255 : 0;
				}
			}
			bufferCtx.putImageData(image, 0, 0);
			ctx.clearRect(0, 0, width, height);
			ctx.imageSmoothingEnabled = false;
			ctx.drawImage(buffer, 0, 0, width, height);
		};

		const loop = () => {
			if (rate <= 0) return;
			frame = requestAnimationFrame(loop);
			const now = performance.now();
			time += Math.min(0.05, Math.max(0, (now - last) / 1000));
			last = now;
			draw();
		};

		// Everything that can throw runs before the loop starts, and any failure
		// removes the canvas: a decorative effect leaves the surface plain.
		let observer: ResizeObserver | null = null;
		try {
			resize();
			draw();
			observer = new ResizeObserver(() => {
				try {
					resize();
				} catch {
					cancelAnimationFrame(frame);
					rate = 0;
					return;
				}
				draw();
			});
			observer.observe(container);
		} catch {
			observer?.disconnect();
			canvas.remove();
			return;
		}
		// A still frame needs no animation loop.
		if (rate > 0) loop();
		return () => {
			cancelAnimationFrame(frame);
			observer?.disconnect();
			canvas.remove();
		};
		// The color is read per frame through a ref. Key the component on it so a
		// theme change redraws a still frame too.
	}, [pixel, arms, speed, center[0], center[1]]);

	return (
		<div
			ref={containerRef}
			aria-hidden="true"
			style={{ position: "relative", width: "100%", height: "100%", overflow: "hidden", ...style }}
		/>
	);
}
