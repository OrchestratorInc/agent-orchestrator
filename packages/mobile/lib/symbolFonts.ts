import type { FontSource } from "expo-font";

/**
 * Faces `AppSymbol` needs loaded before the first frame. None on iOS, where SF
 * Symbols come from the system.
 */
export const symbolFonts: Record<string, FontSource> = {};
