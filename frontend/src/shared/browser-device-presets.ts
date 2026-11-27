// Devices the Browser can show a page as: the panel's device frames, and the
// viewport an `ao browser record --size <preset>` recording lays the page out at.

export type BrowserDevicePreset = {
	id: string;
	label: string;
	width: number;
	height: number;
	category: "phone" | "tablet";
};

export const BROWSER_DEVICE_PRESETS: readonly BrowserDevicePreset[] = [
	{ id: "iphone-se", label: "iPhone SE", width: 375, height: 667, category: "phone" },
	{ id: "iphone-xr", label: "iPhone XR", width: 414, height: 896, category: "phone" },
	{ id: "iphone-12-pro", label: "iPhone 12 Pro", width: 390, height: 844, category: "phone" },
	{ id: "iphone-14-pro-max", label: "iPhone 14 Pro Max", width: 430, height: 932, category: "phone" },
	{ id: "iphone-15-pro-max", label: "iPhone 15 Pro Max", width: 430, height: 932, category: "phone" },
	{ id: "iphone-16-pro-max", label: "iPhone 16 Pro Max", width: 440, height: 956, category: "phone" },
	{ id: "pixel-7", label: "Pixel 7", width: 412, height: 915, category: "phone" },
	{ id: "pixel-8", label: "Pixel 8", width: 412, height: 915, category: "phone" },
	{ id: "pixel-9", label: "Pixel 9", width: 412, height: 924, category: "phone" },
	{ id: "pixel-10", label: "Pixel 10", width: 412, height: 924, category: "phone" },
	{ id: "galaxy-s8-plus", label: "Samsung Galaxy S8+", width: 360, height: 740, category: "phone" },
	{ id: "galaxy-s20-ultra", label: "Samsung Galaxy S20 Ultra", width: 412, height: 915, category: "phone" },
	{ id: "galaxy-a51-71", label: "Samsung Galaxy A51/71", width: 412, height: 914, category: "phone" },
	{ id: "ipad-mini", label: "iPad Mini", width: 768, height: 1024, category: "tablet" },
	{ id: "ipad-air", label: "iPad Air", width: 820, height: 1180, category: "tablet" },
	{ id: "ipad-pro", label: "iPad Pro", width: 1032, height: 1376, category: "tablet" },
	{ id: "surface-pro-7", label: "Surface Pro 7", width: 912, height: 1368, category: "tablet" },
	{ id: "surface-duo", label: "Surface Duo", width: 540, height: 720, category: "phone" },
	{ id: "galaxy-z-fold-5", label: "Galaxy Z Fold 5", width: 344, height: 882, category: "phone" },
	{ id: "zenbook-fold", label: "Asus Zenbook Fold", width: 853, height: 1280, category: "tablet" },
	{ id: "nest-hub", label: "Nest Hub", width: 1024, height: 600, category: "tablet" },
	{ id: "nest-hub-max", label: "Nest Hub Max", width: 1280, height: 800, category: "tablet" },
];

export type BrowserViewportSize = { width: number; height: number; mobile: boolean };

export const MIN_BROWSER_VIEWPORT = 240;
export const MAX_BROWSER_VIEWPORT = 2560;

// Desktop sizes a recording can ask for by name; they lay out as a desktop page.
const DESKTOP_VIEWPORTS: Record<string, { width: number; height: number }> = {
	desktop: { width: 1280, height: 800 },
	laptop: { width: 1440, height: 900 },
	"full-hd": { width: 1920, height: 1080 },
};

/**
 * A viewport from a device preset id, a desktop name, or WIDTHxHEIGHT. Phone
 * and tablet presets lay out as mobile pages (their viewport meta applies);
 * a WIDTHxHEIGHT does when mobile is asked for. Null for anything else.
 */
export function resolveBrowserViewport(size: string, mobile = false): BrowserViewportSize | null {
	const name = size.trim().toLowerCase();
	const preset = BROWSER_DEVICE_PRESETS.find((device) => device.id === name);
	if (preset) return { width: preset.width, height: preset.height, mobile: true };
	const desktop = DESKTOP_VIEWPORTS[name];
	if (desktop) return { ...desktop, mobile };
	const match = /^(\d{2,4})x(\d{2,4})$/.exec(name);
	if (!match) return null;
	const width = Number(match[1]);
	const height = Number(match[2]);
	const inRange = (value: number) => value >= MIN_BROWSER_VIEWPORT && value <= MAX_BROWSER_VIEWPORT;
	return inRange(width) && inRange(height) ? { width, height, mobile } : null;
}
