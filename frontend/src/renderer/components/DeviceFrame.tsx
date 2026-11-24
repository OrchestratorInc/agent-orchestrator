import type { CSSProperties, ReactNode } from "react";
import { cn } from "../lib/utils";

export type DeviceFrameKind = "iphone" | "ipad" | "android-phone" | "android-tablet";

/**
 * The hardware drawn around a device's screen: a brushed metal band, the black
 * glass bezel inside it, and the side buttons. Sizes are fractions of the frame's
 * width so the frame scales with the panel; the screen keeps its own aspect
 * ratio.
 */
type FrameSpec = {
	/** The metal band around the bezel. */
	band: number;
	/** Black bezel between the band and the screen. */
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
		band: 0.02,
		bezel: 0.022,
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
		band: 0.014,
		bezel: 0.036,
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
		band: 0.018,
		bezel: 0.02,
		screenRadius: 0.085,
		buttons: [
			{ edge: "right", at: 0.2, length: 0.065 },
			{ edge: "right", at: 0.3, length: 0.12 },
		],
	},
	"android-tablet": {
		band: 0.014,
		bezel: 0.036,
		screenRadius: 0.03,
		buttons: [
			{ edge: "top", at: 0.12, length: 0.08 },
			{ edge: "top", at: 0.24, length: 0.1 },
		],
	},
};

/** How thick a side button stands out, as a fraction of the frame's width. */
const BUTTON_DEPTH = 0.011;

/**
 * Graphite titanium (like Space Black), the same in both themes like the real hardware. A conic
 * sweep alternates light and dark around the rim the way a curved metal edge
 * reflects a room, so it reads as metal on the dark canvas and the light one.
 */
const METAL_BAND =
	"conic-gradient(from 25deg, #2c2d30, #6f7176 7%, #36383c 15%, #585a5f 24%, #1e1f22 35%, #4b4d52 46%, #84868b 52%, #313337 61%, #606267 71%, #222326 82%, #47494e 92%, #2c2d30)";
/**
 * The rim's profile: a dark machined outer edge, a bright line just inside it
 * where the curve catches the light, and a soft shadow under the device.
 */
const METAL_BAND_SHADOW = [
	"inset 0 0 0 0.75px rgb(0 0 0 / 0.6)",
	"inset 0 0 0 1.75px rgb(255 255 255 / 0.28)",
	"0 1px 2px rgb(0 0 0 / 0.25)",
	"0 20px 44px -18px rgb(0 0 0 / 0.5)",
].join(", ");
/** Where the glass meets the metal: a dark seam that shades the rim's inner edge, and a faint lit edge on the glass. */
const BEZEL_SHADOW = "0 0 0 1px rgb(0 0 0 / 0.75), 0 0 3px 1px rgb(0 0 0 / 0.35), inset 0 0 0 1px rgb(255 255 255 / 0.07)";
/** Side buttons: rounded metal, lit along the middle. */
const METAL_BUTTON = (direction: string) =>
	`linear-gradient(${direction}, #1c1d20 0%, #4c4e53 30%, #85878c 50%, #45474c 70%, #1c1d20 100%)`;

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
	const { band, bezel } = spec;
	const edge = band + bezel;
	// Frame width W: the screen is W(1 - 2·edge) wide, so its height is that
	// over the screen's aspect, and the band and bezel add an edge above and below.
	const frameAspect = 1 / ((1 - 2 * edge) / screen + 2 * edge);
	// Corner radii as fractions of W, then as percentages of each box's own
	// sides so they stay circular at any size.
	const screenRadius = spec.screenRadius * (1 - 2 * edge);
	const bezelRadius = screenRadius + bezel;
	const outerRadius = bezelRadius + band;
	// The bezel box is W(1 - 2·band) wide and H - 2·band·W tall.
	const bezelBoxWidth = 1 - 2 * band;
	const bezelBoxHeight = 1 / frameAspect - 2 * band;
	const frameStyle: CSSProperties = {
		aspectRatio: frameAspect,
		// As large as fits the panel, both ways (the panel is a size container).
		width: `min(100cqw, ${100 * frameAspect}cqh)`,
	};
	return (
		<div className={cn("relative", className)} data-device-frame={kind} style={frameStyle}>
			{spec.buttons.map((button, index) => (
				<span
					aria-hidden="true"
					className="absolute rounded-xs"
					key={index}
					style={{
						...buttonStyle(button, frameAspect),
						background: METAL_BUTTON(button.edge === "top" ? "180deg" : "90deg"),
						boxShadow: "inset 0 0 0 0.5px rgb(0 0 0 / 0.45)",
					}}
				/>
			))}
			<div
				className="absolute inset-0"
				style={{
					background: METAL_BAND,
					boxShadow: METAL_BAND_SHADOW,
					borderRadius: `${outerRadius * 100}% / ${outerRadius * frameAspect * 100}%`,
					padding: `${band * 100}%`,
				}}
			>
				<div
					className="size-full bg-[#050506]"
					style={{
						boxShadow: BEZEL_SHADOW,
						borderRadius: `${(bezelRadius / bezelBoxWidth) * 100}% / ${(bezelRadius / bezelBoxHeight) * 100}%`,
						// Padding percentages are of this box's containing block, the band's content box.
						padding: `${(bezel / bezelBoxWidth) * 100}%`,
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
