/**
 * Lets an artifact page's video play in place, as it does on the desktop.
 * react-native-webview's iOS defaults require a tap before any
 * media plays, muted or not, and send video to the fullscreen player: a page's
 * `<video autoplay muted playsinline>` then never starts, and since iOS loads
 * no frame of a video it has not played, it draws as an empty box.
 *
 * Only for a view the reader can act on: it also lets a page play sound
 * unprompted. Gallery cards take no touches and keep the defaults.
 */
export const ARTIFACT_MEDIA_PLAYBACK = {
	allowsInlineMediaPlayback: true,
	mediaPlaybackRequiresUserAction: false,
} as const;
