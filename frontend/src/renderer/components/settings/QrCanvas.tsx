import { memo, useLayoutEffect, useRef } from "react";
import { encodeQr, modulesForVersion } from "../../lib/qr-matrix";
import { paintQr, quietZonePx } from "./qr-paint";
import { preloadQrLogo, qrLogo, whenQrLogoReady } from "./qr-logo";

// Load the logo as soon as a QR can be shown, not when the first one is drawn.
preloadQrLogo();

/**
 * The QR version every pairing code (and its decoy) is drawn at.
 *
 * Left to itself the encoder picks the smallest version that fits, and a
 * pairing offer's length varies with its endpoints and names: LAN only is
 * version 12, LAN plus a tunnel 15, LAN, Tailscale and a tunnel 17. Each step
 * changed the module count, and with it the drawn size, so the code visibly
 * grew or shrank between generations. Pinning the version keeps the density
 * the same; 17 holds LAN, Tailscale and a tunnel with a typical tunnel name.
 * A longer offer still gets the version it needs (a code that cannot hold its
 * payload is worse than a denser one), and keeps the same outer size because
 * the quiet zone is fixed in px and the modules are not snapped to pixels.
 */
export const PAIRING_QR_VERSION = 17;

/**
 * A QR code drawn on one canvas, synchronously.
 *
 * Encoding and drawing take a few milliseconds, so the code is on screen in
 * the same frame as the component: nothing loads, nothing is cached, and
 * there is no empty state to cover. (The earlier SVG renderer built a clip
 * path of more than a thousand elements per code and drew it after an async
 * image load, which is what made the settings page stutter.)
 *
 * `value` null clears the canvas. `onReady` runs right after a draw, also
 * where no canvas is available (tests), so callers need no special case.
 */
export const QrCanvas = memo(function QrCanvas({
	value,
	size,
	showLogo = true,
	version,
	cache = false,
	className,
	onReady,
	"data-qr-value": dataQrValue,
}: {
	value: string | null;
	/** Side of the square, in CSS px. */
	size: number;
	showLogo?: boolean;
	/** Pin the QR version (see PAIRING_QR_VERSION) so codes of different
	 *  payload lengths draw at the same density and outer size. Omitted, the
	 *  encoder picks the smallest version and the quiet zone follows it. */
	version?: number;
	/** The code is public (a store link, the decoy): keep its encoding for next
	 *  time. Off by default, because a pairing code carries the password. */
	cache?: boolean;
	className?: string;
	onReady?: () => void;
	"data-qr-value"?: string;
}) {
	const ref = useRef<HTMLCanvasElement>(null);
	// Read through a ref so a new callback identity does not redraw the code.
	const onReadyRef = useRef(onReady);
	onReadyRef.current = onReady;

	// Layout effect, so the code is in the first painted frame.
	useLayoutEffect(() => {
		const canvas = ref.current;
		if (!canvas) return undefined;
		const draw = () => {
			const ctx = canvas.getContext("2d");
			if (!ctx) return;
			const dpr = Math.max(1, window.devicePixelRatio || 1);
			const pixels = Math.round(size * dpr);
			// Resizing the backing store clears it; either way the draw below covers it.
			if (canvas.width !== pixels) {
				canvas.width = pixels;
				canvas.height = pixels;
			}
			ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
			if (value === null) {
				ctx.clearRect(0, 0, size, size);
				return;
			}
			const matrix = encodeQr(value, { version, cache });
			// The quiet zone follows the pinned version, so a longer payload that
			// needs a bigger one keeps the same outer box.
			const margin = quietZonePx(size, modulesForVersion(version ?? matrix.version));
			paintQr(ctx, matrix, { size, margin, showLogo, logo: showLogo ? qrLogo() : null });
		};
		let cancelLogoWait: (() => void) | undefined;
		try {
			draw();
			// Drawn before the logo finished loading: draw once more when it has.
			if (value !== null && showLogo && !qrLogo()) {
				cancelLogoWait = whenQrLogoReady(() => {
					try {
						draw();
					} catch {
						// Keep the code as drawn.
					}
				});
			}
		} catch {
			// The code is purely visual: where it cannot be drawn, fail silent
			// rather than crash the settings page.
		}
		if (value !== null) onReadyRef.current?.();
		return () => cancelLogoWait?.();
	}, [value, size, showLogo, version, cache]);

	return (
		<canvas
			ref={ref}
			aria-hidden="true"
			className={className}
			data-qr-value={dataQrValue}
			style={{ width: size, height: size }}
		/>
	);
});
