import type { ReactNode } from "react";
import { truncateMiddle } from "../lib/utils";

/**
 * Shows `text` shortened in the middle (see truncateMiddle). The hidden middle
 * is often what tells two values apart, such as two Amp builds, so the full
 * value stays in the hover title and is what screen readers announce.
 * `format` wraps the value, e.g. into a translated sentence.
 */
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
