import type { AndroidSymbol } from "expo-symbols";
import { symbols, type IOSSymbol } from "../symbols";

/**
 * The glyphs the Files screens hand to native controls: header and toolbar
 * items, their menus, the row context menu and the review button. Each names
 * its SF Symbol and its Material Symbol, whose drawable ./fileIcons.android
 * bundles.
 */
export const fileIconSymbols = {
	previous: symbols.chevronUp,
	next: symbols.chevronDown,
	more: symbols.more,
	wrapLines: symbols.wrapLines,
	copy: symbols.copy,
	share: symbols.share,
	openDiff: symbols.diff,
	openFile: symbols.fileText,
	preview: { ios: "eye", android: "visibility" },
	source: { ios: "chevron.left.forwardslash.chevron.right", android: "code" },
	review: { ios: "text.bubble", android: "rate_review" },
	delete: symbols.delete,
} as const satisfies Record<string, { ios: IOSSymbol; android: AndroidSymbol }>;

export type FileIconName = keyof typeof fileIconSymbols;
