/** A fenced block's language that names a Mermaid diagram. */
export function isMermaidFence(language: string | undefined): boolean {
	return language?.trim().toLowerCase() === "mermaid";
}
