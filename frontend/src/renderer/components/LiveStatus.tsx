import { useEffect, useState } from "react";

export function LiveStatus({ text }: { text: string }) {
	const [spoken, setSpoken] = useState("");
	useEffect(() => setSpoken(text), [text]);
	return <p aria-atomic="true" aria-live="polite" className="sr-only" role="status">{spoken}</p>;
}
