// Per-harness brand marks, copied from the desktop by scripts/sync-harness-logos.mjs.
//
// The desktop is the source of truth (frontend/src/renderer/components/AgentAvatar.tsx).
// Where a brand's mark only reads on one background, it ships a light/dark pair
// and each theme draws its own, so no mark needs a backdrop chip behind it.
//
// Pure data + rules, no React, so the registry can be unit-tested. The `require`
// calls live in harnessLogoAssets.ts, because Node cannot require a PNG.

import { HARNESS_LOGO_MANIFEST } from "./harnessLogoManifest";

/** Every harness that ships a mark, as the desktop does. */
export const LOGO_KEYS: ReadonlySet<string> = new Set(Object.keys(HARNESS_LOGO_MANIFEST));

/** Normalised lookup key, or "" when there is no usable harness. */
export function logoKey(harness?: string | null): string {
	return harness?.trim().toLowerCase() ?? "";
}

/** Whether a mark exists for this harness. */
export function hasLogo(harness?: string | null): boolean {
	return LOGO_KEYS.has(logoKey(harness));
}

/**
 * Fallback for a harness with no mark.
 *
 * Desktop's `toAgentProvider()` funnels every unrecognised harness into
 * `default: return "codex"`, so an unknown agent renders as the Codex logo —
 * confidently wrong. An initial says "some agent I don't have a mark for",
 * which is true. The fake harness intentionally has no brand asset and lands
 * here.
 */
export function harnessInitial(harness?: string | null): string {
	const key = harness?.trim();
	if (!key) return "?";
	return key.charAt(0).toUpperCase();
}
