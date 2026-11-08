import type { ReactElement } from "react";

/**
 * Where neither Liquid Glass (iOS 26, `composer-glass.ios`) nor Android's
 * stand-in sheet (`composer-glass.android`) draws, the composer keeps the
 * elevated fill — see `ChatComposer`'s pill styles.
 */
export const composerGlassSupported = false;

export function ComposerGlass(_props: { radius: number }): ReactElement | null {
	return null;
}
