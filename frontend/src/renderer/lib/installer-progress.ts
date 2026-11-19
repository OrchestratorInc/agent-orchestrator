// The shared installer marks user-facing progress separately from diagnostic
// output. Only show the latest marked line while installation is active.
export function installerProgress(output: string | undefined): string | undefined {
	const prefix = "[installer] ";
	return output?.split("\n").reverse().find((line) => line.startsWith(prefix))?.slice(prefix.length);
}
