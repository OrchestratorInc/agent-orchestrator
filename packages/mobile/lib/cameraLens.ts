// Picks which rear lens the QR scanner should use on iOS. Pure, so it can be
// unit-tested without a camera — mirroring `pushStatus.ts`.
//
// expo-camera's `selectedLens` is matched natively against
// AVCaptureDevice.localizedName, NOT against an AVCaptureDevice.DeviceType
// identifier (see CameraSessionManager.updateDevice: `$0.localizedName ==
// delegate.selectedLens`). getAvailableLensesAsync() likewise returns
// localizedName strings. Passing "builtInWideAngleCamera" therefore matches
// nothing and silently falls back to the default device.
//
// That default is the problem this solves: iOS hands back a *virtual* device
// ("Back Dual Wide Camera" / "Back Triple Camera") whose widest member is the
// 0.5x ultra-wide, so the preview opens zoomed out. Ultra-wide is the worst lens
// for scanning — the code covers fewer pixels, so it must be held much closer,
// and the ultra-wide's longer minimum focus distance makes close-ups soft.

/** localizedName of the plain 1x rear lens on an English-locale device. */
export const NORMAL_LENS = "Back Camera";

// Words that mark a lens as something other than the plain 1x optic. Used only
// by the non-English fallback below.
const QUALIFIERS = ["ultra", "telephoto", "dual", "triple", "lidar", "truedepth", "continuity", "desk"];

/**
 * Chooses the normal (1x) lens from the localizedName list the camera reports,
 * or undefined to keep whatever the native default is.
 *
 * Returning undefined rather than guessing matters: `selectedLens` that matches
 * no device leaves the session without a camera, so a wrong answer is worse than
 * no answer.
 */
export function pickNormalLens(lenses: string[]): string | undefined {
	if (lenses.includes(NORMAL_LENS)) return NORMAL_LENS;
	// localizedName is localized, so the exact match above fails on a non-English
	// device. Every specialised lens carries a qualifier the plain one lacks, so
	// drop those and take the shortest remaining name.
	const plain = lenses
		.filter((l) => {
			const lower = l.toLowerCase();
			return !QUALIFIERS.some((q) => lower.includes(q));
		})
		.sort((a, b) => a.length - b.length);
	return plain[0];
}
