/**
 * A project's mark on its card: its initials on a colour picked from its name,
 * so the same project keeps the same colour on every phone and every launch.
 *
 * The hues are their own set, apart from the theme's status colours: a project
 * tinted the "failing" red or "needs you" amber would read as a state it is not.
 * Each is dark enough for white initials in both themes.
 */
const AVATAR_HUES = ["#6e56cf", "#0d74ce", "#0f8a7e", "#c2298a", "#d0490f", "#2f7d4f", "#0e7490", "#5a6b85"] as const;

/** Up to two letters: the first of each of the name's first two words, else its first letter. */
export function projectInitials(name: string): string {
	const words = name.trim().split(/[\s._\-/]+/).filter(Boolean);
	if (!words.length) return "?";
	const letters = words.length > 1 ? words[0][0] + words[1][0] : words[0][0];
	return letters.toUpperCase();
}

/** The same name always gets the same hue. */
export function projectHue(name: string): string {
	let hash = 0;
	for (const char of name) hash = (hash * 31 + char.charCodeAt(0)) | 0;
	return AVATAR_HUES[Math.abs(hash) % AVATAR_HUES.length];
}
