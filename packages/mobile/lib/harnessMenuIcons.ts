import { logoKey } from "./harnessLogo";

// The harness marks the installed iOS binaries carry in their asset catalog.
//
// SwiftUI menus draw a mark by asset name (`HarnessLogo-<key>`), and those image
// sets are copied into the catalog at prebuild, so they arrive only with a store
// build. A JS update (OTA) can reach a binary whose catalog predates a harness;
// asking it for a missing asset draws an empty slot, so the menus draw the
// terminal symbol for any harness not listed here.
//
// Frozen at the version below. The store build that bumps `version` in app.json
// ships every image set under assets/icons/harness, so when it does,
// harnessMenuIcons.source.test.ts fails until this list is set to every
// harness with a mark and MENU_ICONS_APP_VERSION to the new version.
export const MENU_ICONS_APP_VERSION = "2.4.0";

export const MENU_ICON_KEYS: ReadonlySet<string> = new Set([
	"agy", "aider", "amp", "auggie", "autohand", "claude-code", "cline", "codex",
	"continue", "copilot", "crush", "cursor", "devin", "droid", "fx", "goose",
	"grok", "kilocode", "kimchi", "kimi", "kiro", "muse", "opencode", "pi",
	"prime-agent", "qwen", "vibe",
]);

/** Whether the running binary's asset catalog has this harness's menu mark. */
export function hasMenuIcon(harness?: string | null): boolean {
	return MENU_ICON_KEYS.has(logoKey(harness));
}
