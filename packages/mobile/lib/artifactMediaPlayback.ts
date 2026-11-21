/**
 * Lets an artifact page's video play in place, as it does on the desktop.
 * react-native-webview's iOS defaults require a tap before any
 * media plays, muted or not, and send video to the fullscreen player: a page's
 * `<video autoplay muted playsinline>` then never starts, and since iOS loads
 * no frame of a video it has not played, it draws as an empty box.
 *
 * Only for a page the reader is looking at: it also lets a page play sound
 * unprompted. Gallery cards take no touches and keep the defaults, and so do
 * the viewer's neighbouring pages (see artifactPagePlaysMedia).
 */
export const ARTIFACT_MEDIA_PLAYBACK = {
	allowsInlineMediaPlayback: true,
	mediaPlaybackRequiresUserAction: false,
} as const;

// A media element, or script that makes sound without one.
const MEDIA = /<(?:video|audio)\b|\bAudio\s*\(|AudioContext/i;

/**
 * Whether a viewer page gets ARTIFACT_MEDIA_PLAYBACK: only while it is the page
 * on screen, and only if its markup has media (`html` undefined: a page loaded
 * from its address, which can't be read first). WebView reads the policy when
 * it is created, so the page is keyed on this: swiped away, it loads again
 * under the defaults, which play nothing with sound, and its sound stops. A
 * page with no media never reloads; one that hides its media from this check
 * keeps the defaults, the safe side.
 */
export function artifactPagePlaysMedia(active: boolean, html: string | undefined): boolean {
	return active && (html === undefined || MEDIA.test(html));
}
