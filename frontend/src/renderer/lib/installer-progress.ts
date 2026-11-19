// Shared installer progress is the latest output line; vendor output takes over
// once execution starts, so a stale download message never masks its progress.
export function installerProgress(output: string | undefined): string | undefined {
	const prefix = "[installer] ";
	const line = output?.split("\n").filter((part) => part.trim()).at(-1);
	return line?.startsWith(prefix) ? line.slice(prefix.length) : undefined;
}
