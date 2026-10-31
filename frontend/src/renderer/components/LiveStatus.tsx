import { useEffect, useState } from "react";

/**
 * A polite screen reader announcement that stays mounted and swaps its text.
 * A live region that mounts together with its text is often not announced
 * (VoiceOver especially), so the region mounts empty and the text goes in
 * once it is in the page. It is not shown; the visible copy stays where it is.
 */
export function LiveStatus({ text }: { text: string }) {
	const [spoken, setSpoken] = useState("");
	useEffect(() => setSpoken(text), [text]);
	return <p aria-atomic="true" aria-live="polite" className="sr-only" role="status">{spoken}</p>;
}
