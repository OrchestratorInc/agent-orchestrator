import type { FontSource } from "expo-font";
import regular from "expo-symbols/androidWeights/regular";

/**
 * Faces `AppSymbol` needs loaded before the first frame.
 *
 * Android draws Material Symbols from a bundled font, and expo-symbols draws an
 * empty box until that font has loaded. Loading it with the app's own faces,
 * under the splash, keeps every icon from appearing a beat after its screen.
 * The key is the family name expo-symbols loads by, so its own load finds the
 * face already registered and resolves at once.
 */
export const symbolFonts: Record<string, FontSource> = { [regular.name]: regular.font };
