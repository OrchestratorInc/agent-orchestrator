/** The minimum tappable square: 48dp, from Material 3. */
export const touchTarget: number = 48;

/**
 * A form sheet's top corners: Material 3's bottom-sheet shape (extra large,
 * 28dp). react-native-screens has no system shape to fall back to on Android
 * and draws an unset radius square.
 */
export const sheetCornerRadius: number | undefined = 28;
