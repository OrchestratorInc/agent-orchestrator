import type { FeatherIconName } from "./icons";
import { symbols, type AppSymbolName, type PlatformSymbol } from "./symbols";

/**
 * An icon prop on a shared control: a registry name (`"refresh"`), a one-off
 * `{ ios, android }` pair, or a Lucide name.
 *
 * Lucide names are still accepted because the controls took them first. The
 * chrome ones among them draw as their system symbol (see `lucideSymbols`), so
 * a call site written against the old prop gets SF Symbols and Material Symbols
 * without changing; a name with no system equivalent, a domain glyph such as a
 * pull request, stays a Lucide drawing.
 */
export type ChromeIcon = AppSymbolName | PlatformSymbol | FeatherIconName;

export type ResolvedChromeIcon = { symbol: PlatformSymbol; lucide?: undefined } | { symbol?: undefined; lucide: FeatherIconName };

/**
 * The system symbol for each Lucide name the shared controls are given. Drawn
 * for the same job rather than the same picture, so a status keeps its meaning
 * on both platforms.
 */
const lucideSymbols: Partial<Record<FeatherIconName, PlatformSymbol>> = {
	"refresh-cw": symbols.refresh,
	plus: symbols.add,
	x: symbols.close,
	check: symbols.checkmark,
	"check-circle": symbols.checkCircle,
	"list-tree": { ios: "list.bullet.indent", android: "account_tree" },
	link: { ios: "link", android: "link" },
	"message-circle": symbols.chat,
	"file-diff": symbols.diff,
	"alert-triangle": symbols.warning,
	"alert-circle": symbols.error,
	"wifi-off": symbols.offline,
	unplug: symbols.disconnected,
	moon: symbols.idle,
	monitor: symbols.desktop,
	"monitor-smartphone": symbols.devices,
	// The connection failures each keep a glyph of their own, so a rejected
	// password does not look like a machine that is simply out of range.
	"monitor-off": { ios: "desktopcomputer.trianglebadge.exclamationmark", android: "desktop_access_disabled" },
	"monitor-cog": { ios: "exclamationmark.octagon", android: "report" },
	"route-off": { ios: "point.topleft.down.curvedto.point.bottomright.up", android: "route" },
	"download-cloud": symbols.update,
};

// Own keys only: a name such as `constructor` would otherwise find the
// prototype's function on either table.
function has<K extends string>(table: object, key: string): key is K {
	return Object.prototype.hasOwnProperty.call(table, key);
}

/** Which glyph to draw for an icon prop. A registry name wins over a Lucide name spelled the same way. */
export function resolveChromeIcon(icon: ChromeIcon): ResolvedChromeIcon {
	if (typeof icon !== "string") return { symbol: icon };
	if (has<AppSymbolName>(symbols, icon)) return { symbol: symbols[icon] };
	const mapped = has<FeatherIconName>(lucideSymbols, icon) ? lucideSymbols[icon] : undefined;
	return mapped ? { symbol: mapped } : { lucide: icon };
}
