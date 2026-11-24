import type { CSSProperties, ReactNode } from "react";
import { cn } from "../lib/utils";

export type DeviceFrameKind = "iphone" | "ipad" | "android-phone" | "android-tablet";

/**
 * The hardware drawn around a device's screen: flat, the device's own black,
 * with its side buttons. Sizes are fractions of the frame's width so the frame
 * scales with the panel; the screen keeps its own aspect ratio.
 */
type FrameSpec = {
	/** Bezel around the screen. */
	bezel: number;
	/** Screen corner radius, as a fraction of the screen's width. */
	screenRadius: number;
	/** Side buttons: which edge, where along it and how long (fractions of the frame's height or width). */
	buttons: { edge: "left" | "right" | "top"; at: number; length: number }[];
};

const FRAMES: Record<DeviceFrameKind, FrameSpec> = {
	// A recent iPhone: a thin even bezel, deep screen corners, the action
	// button and volume keys on the left, the side button on the right.
	iphone: {
		bezel: 0.032,
		screenRadius: 0.13,
		buttons: [
			{ edge: "left", at: 0.17, length: 0.045 },
			{ edge: "left", at: 0.245, length: 0.075 },
			{ edge: "left", at: 0.335, length: 0.075 },
			{ edge: "right", at: 0.27, length: 0.11 },
		],
	},
	// An iPad: a wider even bezel, gentle corners, the top button and volume keys.
	ipad: {
		bezel: 0.045,
		screenRadius: 0.035,
		buttons: [
			{ edge: "top", at: 0.78, length: 0.09 },
			{ edge: "right", at: 0.08, length: 0.06 },
			{ edge: "right", at: 0.15, length: 0.06 },
		],
	},
	// An Android phone (Pixel-like): thin bezel, rounder corners than a
	// tablet, power and volume on the right.
	"android-phone": {
		bezel: 0.03,
		screenRadius: 0.085,
		buttons: [
			{ edge: "right", at: 0.2, length: 0.065 },
			{ edge: "right", at: 0.3, length: 0.12 },
		],
	},
	"android-tablet": {
		bezel: 0.045,
		screenRadius: 0.03,
		buttons: [
			{ edge: "top", at: 0.12, length: 0.08 },
			{ edge: "top", at: 0.24, length: 0.1 },
		],
	},
};

/** How thick a side button stands out, as a fraction of the frame's width. */
const BUTTON_DEPTH = 0.011;

/** Screen width ÷ height to assume until the first frame says otherwise. */
const DEFAULT_SCREEN_ASPECT: Record<DeviceFrameKind, number> = {
	iphone: 1206 / 2622,
	ipad: 1668 / 2420,
	"android-phone": 1080 / 2400,
	"android-tablet": 1600 / 2560,
};

/**
 * Which frame a device gets: from its platform, its name ("iPad Pro…",
 * "Pixel Tablet"), and once frames arrive the screen's shape, since a tablet's
 * portrait screen is much wider than a phone's.
 */
export function deviceFrameKind(platform: "ios" | "android", name: string, screenAspect?: number): DeviceFrameKind {
	const portrait = screenAspect === undefined ? undefined : Math.min(screenAspect, 1 / screenAspect);
	const tablet = /\b(ipad|tablet)\b/i.test(name) || (portrait !== undefined && portrait > 0.6);
	if (platform === "ios") return tablet ? "ipad" : "iphone";
	return tablet ? "android-tablet" : "android-phone";
}

export function DeviceFrame({
	children,
	className,
	kind,
	screenAspect,
}: {
	/** The screen: fills the frame's screen area, clipped to its corners. */
	children: ReactNode;
	className?: string;
	kind: DeviceFrameKind;
	/** Screen width ÷ height; the kind's usual shape until known. */
	screenAspect?: number;
}) {
	const spec = FRAMES[kind];
	const screen = screenAspect ?? DEFAULT_SCREEN_ASPECT[kind];
	const { bezel } = spec;
	// Frame width W: the screen is W(1 - 2·bezel) wide, so its height is that
	// over the screen's aspect, and the frame adds a bezel above and below.
	const frameAspect = 1 / ((1 - 2 * bezel) / screen + 2 * bezel);
	// Corner radii as percentages of each side, so they stay circular at any size.
	const screenRadius = spec.screenRadius * (1 - 2 * bezel);
	const outerRadius = screenRadius + bezel;
	const frameStyle: CSSProperties = {
		aspectRatio: frameAspect,
		// As large as fits the panel, both ways (the panel is a size container).
		width: `min(100cqw, ${100 * frameAspect}cqh)`,
	};
	return (
		<div className={cn("relative", className)} data-device-frame={kind} style={frameStyle}>
			{spec.buttons.map((button, index) => (
				<span aria-hidden="true" className="absolute rounded-xs bg-black ring-1 ring-[var(--color-border-strong)]" key={index} style={buttonStyle(button, frameAspect)} />
			))}
			<div
				className="absolute inset-0 bg-black ring-1 ring-[var(--color-border-strong)]"
				style={{
					borderRadius: `${outerRadius * 100}% / ${outerRadius * frameAspect * 100}%`,
					padding: `${bezel * 100}%`,
				}}
			>
				<div
					className="relative size-full overflow-hidden bg-black"
					style={{ borderRadius: `${spec.screenRadius * 100}% / ${spec.screenRadius * screen * 100}%` }}
				>
					{children}
				</div>
			</div>
		</div>
	);
}

/** A side button just outside the body, on its edge. */
function buttonStyle(button: FrameSpec["buttons"][number], frameAspect: number): CSSProperties {
	const depth = `${BUTTON_DEPTH * 100}%`;
	if (button.edge === "top") {
		return { top: `-${BUTTON_DEPTH * frameAspect * 100}%`, left: `${button.at * 100}%`, width: `${button.length * 100}%`, height: `${BUTTON_DEPTH * frameAspect * 100 * 2}%` };
	}
	return {
		top: `${button.at * 100}%`,
		height: `${button.length * 100}%`,
		width: `calc(${depth} * 2)`,
		[button.edge]: `-${depth}`,
	};
}
