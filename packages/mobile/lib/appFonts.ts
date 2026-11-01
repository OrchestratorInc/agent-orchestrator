import type { FontSource } from "expo-font";
import { font } from "./tokens";

/**
 * The app's text family, DM Sans (T3 Code's), loaded under the splash. The keys
 * are the aliases in tokens' `font`, which React Native text resolves; SwiftUI
 * text finds the same files by their PostScript names (see `swiftUIFont.ts`).
 * The faces are bundled rather than installed from npm: assets/fonts/dm-sans,
 * under the SIL Open Font License beside them.
 */
export const appFonts: Record<string, FontSource> = {
	[font.regular]: require("../assets/fonts/dm-sans/DMSans_400Regular.ttf"),
	[font.medium]: require("../assets/fonts/dm-sans/DMSans_500Medium.ttf"),
	[font.semibold]: require("../assets/fonts/dm-sans/DMSans_600SemiBold.ttf"),
};
