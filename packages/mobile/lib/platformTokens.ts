/**
 * The measurements that differ by platform. Metro picks
 * `platformTokens.android.ts` on Android; splitting by file rather than calling
 * `Platform.select` keeps `tokens.ts` free of React Native imports, so every
 * module and test that reads a token can load it without a native mock.
 */

/** The minimum tappable square: 44pt, from the HIG. */
export const touchTarget: number = 44;

/**
 * A form sheet's top corners. Unset on iOS, so UIKit draws its own sheet shape;
 * see the Android file for why that one names a value.
 */
export const sheetCornerRadius: number | undefined = undefined;

/**
 * An inset-grouped list drawn in React Native: iOS 26's List section, one
 * rounded card whose rows meet flush, divided by hairlines.
 */
export const groupedCorner: { outer: number; inner: number; gap: number } = { outer: 26, inner: 0, gap: 0 };
