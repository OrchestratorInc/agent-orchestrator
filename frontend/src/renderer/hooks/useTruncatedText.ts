import { useEffect, useRef, useState } from "react";

export function useTruncatedText<T extends HTMLElement>(text: string) {
	const ref = useRef<T | null>(null);
	const [isTruncated, setIsTruncated] = useState(false);

	useEffect(() => {
		const el = ref.current;
		if (!el) return;
		const update = () => setIsTruncated(el.scrollWidth > el.clientWidth);
		update();
		const observer = new ResizeObserver(update);
		observer.observe(el);
		return () => observer.disconnect();
	}, [text]);

	return { ref, isTruncated };
}
