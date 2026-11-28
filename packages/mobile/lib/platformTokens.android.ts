/** The minimum tappable square: 48dp, from Material 3. */
export const touchTarget: number = 48;

/**
 * A form sheet's top corners: Material 3's bottom-sheet shape (extra large,
 * 28dp). react-native-screens has no system shape to fall back to on Android
 * and draws an unset radius square.
 */
export const sheetCornerRadius: number | undefined = 28;

/**
 * An inset-grouped list drawn in React Native: Material 3's connected list,
 * the shape composeGroupedList draws in Compose. Fully rounded at a section's
 * ends, slightly between rows, with a small gap in place of dividers.
 * `radius.xl`, `radius.xs` and `space.hair`, as numbers: tokens.ts imports
 * this file, so it cannot import them back.
 */
export const groupedCorner: { outer: number; inner: number; gap: number } = { outer: 20, inner: 4, gap: 2 };
