// How much browser stream the phone asks the desktop for: JPEG quality and a
// frame-rate cap, picked from the network it is on and the bandwidth the
// stream actually uses (StreamStats). Over budget it gives up quality first
// (the page stays readable longer at a lower quality than at a lower frame
// rate), then frames; with room to spare it takes frames back first.

export type StreamQuality = { quality: number; maxFps: number };
export type StreamLink = "cellular" | "wifi" | "unknown";

const START: Record<StreamLink, StreamQuality> = {
	cellular: { quality: 50, maxFps: 15 },
	wifi: { quality: 70, maxFps: 30 },
	unknown: { quality: 60, maxFps: 20 },
};
const CEILING: Record<StreamLink, StreamQuality> = {
	cellular: { quality: 60, maxFps: 20 },
	wifi: { quality: 80, maxFps: 30 },
	unknown: { quality: 70, maxFps: 30 },
};
// Kilobytes per second the stream may use on each link.
const BUDGET_KBPS: Record<StreamLink, number> = { cellular: 150, wifi: 1_000, unknown: 400 };
const MIN_QUALITY = 40;
const MIN_FPS = 5;

/** expo-network's NetworkStateType, as the stream sees it. */
export function streamLink(type: string | undefined): StreamLink {
	if (type === "CELLULAR") return "cellular";
	if (type === "WIFI" || type === "ETHERNET") return "wifi";
	return "unknown";
}

export function initialStreamQuality(link: StreamLink): StreamQuality {
	return { ...START[link] };
}

/** The next quality for a stream using `kbps` on `link`; the same object when nothing changes. */
export function adjustStreamQuality(current: StreamQuality, kbps: number, link: StreamLink): StreamQuality {
	const ceiling = CEILING[link];
	const budget = BUDGET_KBPS[link];
	let { quality, maxFps } = current;
	if (kbps > budget) {
		if (quality > MIN_QUALITY) quality = Math.max(MIN_QUALITY, quality - 10);
		else maxFps = Math.max(MIN_FPS, maxFps - 5);
	} else if (kbps < budget / 2) {
		if (maxFps < ceiling.maxFps) maxFps = Math.min(ceiling.maxFps, maxFps + 5);
		else if (quality < ceiling.quality) quality = Math.min(ceiling.quality, quality + 10);
	}
	quality = Math.min(quality, ceiling.quality);
	maxFps = Math.min(maxFps, ceiling.maxFps);
	return quality === current.quality && maxFps === current.maxFps ? current : { quality, maxFps };
}
