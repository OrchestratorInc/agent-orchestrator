export function posixShellQuote(value: string): string {
	return `'${value.replace(/'/g, "'\\''")}'`;
}

export function droppedPathsText(paths: readonly string[], sandbox: boolean): string {
	return `${paths.map((path) => (sandbox ? posixShellQuote(path) : /\s/.test(path) ? `'${path}'` : path)).join(" ")} `;
}
