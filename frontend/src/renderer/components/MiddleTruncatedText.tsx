import type { ReactNode } from "react";
import { truncateMiddle } from "../lib/utils";

export function MiddleTruncatedText({ text, format = (value) => value, className }: {
	text: string;
	format?: (value: string) => ReactNode;
	className?: string;
}) {
	const shortened = truncateMiddle(text);
	if (shortened === text) return <span className={className}>{format(text)}</span>;
	return <span className={className} title={text}>
		<span aria-hidden="true">{format(shortened)}</span>
		<span className="sr-only">{format(text)}</span>
	</span>;
}
