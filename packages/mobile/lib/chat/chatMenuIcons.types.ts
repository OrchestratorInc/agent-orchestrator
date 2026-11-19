import { symbols, type IOSSymbol } from "../symbols";
import type { MaterialDrawableName } from "../toolbarIcon.types";

/**
 * The glyphs the chat's native menus draw beside their actions, by job: the SF
 * Symbol iOS's menu resolves by name, and the Material Symbol Android's menu
 * draws from the shared drawable registry.
 */
export const chatMenuSymbols = {
	edit: symbols.rename,
	copy: symbols.copy,
	photo: symbols.photo,
	file: symbols.file,
	model: { ios: "square.stack.3d.up", android: "stacks" },
	effort: { ios: "gauge.medium", android: "speed" },
	fast: { ios: "bolt", android: "bolt" },
	permissions: { ios: "checkmark.shield", android: "verified_user" },
	setting: { ios: "slider.horizontal.3", android: "tune" },
} as const satisfies Record<string, { ios: IOSSymbol; android: MaterialDrawableName }>;

export type ChatMenuIcon = keyof typeof chatMenuSymbols;
