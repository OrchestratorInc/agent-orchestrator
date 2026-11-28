import { useEffect, useState } from "react";

/** Whether the person can see this window: it is visible and has focus. */
export function useWindowAttention(): boolean {
	const [attentive, setAttentive] = useState(windowAttentive);
	useEffect(() => {
		const update = () => setAttentive(windowAttentive());
		window.addEventListener("focus", update);
		window.addEventListener("blur", update);
		document.addEventListener("visibilitychange", update);
		update();
		return () => {
			window.removeEventListener("focus", update);
			window.removeEventListener("blur", update);
			document.removeEventListener("visibilitychange", update);
		};
	}, []);
	return attentive;
}

function windowAttentive(): boolean {
	return document.visibilityState === "visible" && document.hasFocus();
}
