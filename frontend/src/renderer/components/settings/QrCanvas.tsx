import { memo, useLayoutEffect, useRef } from "react";
import { encodeQr, modulesForVersion } from "../../lib/qr-matrix";
import { paintQr, quietZonePx } from "./qr-paint";
import { preloadQrLogo, qrLogo, whenQrLogoReady } from "./qr-logo";

preloadQrLogo();

export const PAIRING_QR_VERSION = 17;

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
	size: number;
	showLogo?: boolean;
	version?: number;
	cache?: boolean;
	className?: string;
	onReady?: () => void;
	"data-qr-value"?: string;
}) {
	const ref = useRef<HTMLCanvasElement>(null);
	const onReadyRef = useRef(onReady);
	onReadyRef.current = onReady;

	useLayoutEffect(() => {
		const canvas = ref.current;
		if (!canvas) return undefined;
		const draw = () => {
			const ctx = canvas.getContext("2d");
			if (!ctx) return;
			const dpr = Math.max(1, window.devicePixelRatio || 1);
			const pixels = Math.round(size * dpr);
			if (canvas.width !== pixels || canvas.height !== pixels) {
				canvas.width = pixels;
				canvas.height = pixels;
			}
			ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
			if (value === null) {
				ctx.clearRect(0, 0, size, size);
				return;
			}
			const matrix = encodeQr(value, { version, cache });
			const margin = quietZonePx(size, modulesForVersion(version ?? matrix.version));
			paintQr(ctx, matrix, { size, margin, showLogo, logo: showLogo ? qrLogo() : null });
		};
		let cancelLogoWait: (() => void) | undefined;
		try {
			draw();
			if (value !== null && showLogo && !qrLogo()) {
				cancelLogoWait = whenQrLogoReady(() => {
					try {
						draw();
					} catch {
					}
				});
			}
		} catch {
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
