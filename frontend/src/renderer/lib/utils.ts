import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

export function cn(...inputs: ClassValue[]) {
	return twMerge(clsx(inputs));
}

/**
 * Shortens a long identifier in the middle, keeping its start and end, so
 * "v0.0.1792022479-ge66e24" reads "v0.0.179…6e24". Text that already fits is
 * returned unchanged. Show the full value in a title or tooltip alongside.
 */
export function truncateMiddle(text: string, head = 8, tail = 4): string {
	if (text.length <= head + tail + 1) return text;
	return `${text.slice(0, head)}…${text.slice(-tail)}`;
}
