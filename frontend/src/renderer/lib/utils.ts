import { clsx, type ClassValue } from "clsx";
import { extendTailwindMerge } from "tailwind-merge";

const twMerge = extendTailwindMerge({
	extend: {
		theme: {
			text: ["micro", "2xs", "caption", "sm-md", "md-sm", "control", "brand", "subtitle", "heading-sm", "heading", "heading-lg"],
		},
	},
});

export function cn(...inputs: ClassValue[]) {
	return twMerge(clsx(inputs));
}

export function truncateMiddle(text: string, head = 8, tail = 4): string {
	if (text.length <= head + tail + 1) return text;
	return `${text.slice(0, head)}…${tail > 0 ? text.slice(-tail) : ""}`;
}
