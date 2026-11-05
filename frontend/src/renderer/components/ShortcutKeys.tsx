import "./shortcut-keys.css";

// Modifier glyphs and arrows need spoken names in accessible labels.
const spokenKeys: Record<string, string> = {
	"⌘": "Command",
	"⌥": "Option",
	"⌃": "Control",
	"⇧": "Shift",
	"↑": "Up",
	"↓": "Down",
	"←": "Left",
	"→": "Right",
	"1-9": "1 through 9",
	Esc: "Escape",
};

export function shortcutKeysDescription(keys: readonly string[]): string {
	return keys.map((key) => spokenKeys[key] ?? key).join(" + ");
}

export function ShortcutKeys({ keys }: { keys: readonly string[] }) {
	return (
		<span className="shortcut-keys" role="img" aria-label={shortcutKeysDescription(keys)}>
			{keys.map((key, index) => (
				<kbd key={`${key}-${index}`} aria-hidden="true">{key === "1-9" ? "1–9" : key}</kbd>
			))}
		</span>
	);
}
