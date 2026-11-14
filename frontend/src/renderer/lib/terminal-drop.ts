// What a file dropped on a terminal types at its prompt: the path the file was
// put at, as a native terminal types a dropped file's path.

/**
 * A value quoted for a POSIX shell: in single quotes, with each embedded
 * single quote written as '\''. Nothing inside single quotes is expanded, so
 * $(…), backticks, ;, &, | and spaces all reach the program as written.
 */
export function posixShellQuote(value: string): string {
	return `'${value.replace(/'/g, "'\\''")}'`;
}

/**
 * The text typed for the dropped files' paths, with a trailing space. A
 * sandbox path names the file as the person called it, so any character can
 * reach the shell: it is always quoted. A local path is one the main process
 * named from [\w.-] under the app's own folder, and the local shell may not be
 * POSIX (Windows), so it keeps the quotes it has always had, around whitespace.
 */
export function droppedPathsText(paths: readonly string[], sandbox: boolean): string {
	return `${paths.map((path) => (sandbox ? posixShellQuote(path) : /\s/.test(path) ? `'${path}'` : path)).join(" ")} `;
}
