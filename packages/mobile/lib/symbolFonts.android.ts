import type { FontSource } from "expo-font";
import regular from "expo-symbols/androidWeights/regular";

/**
 * Faces `AppSymbol` needs loaded before the first frame.
 *
 * Android draws Material Symbols from a bundled font. Loading it with the app's
 * own faces, under the splash, means the face is in by the time any screen
 * renders, so `AppSymbol` draws each glyph on its first render instead of
 * waiting on expo-symbols' own load, which paints an empty box first. The key
 * is the family name expo-symbols loads by, so a fallback `SymbolView` finds
 * the face already registered too.
 */
export const symbolFonts: Record<string, FontSource> = { [regular.name]: regular.font };
