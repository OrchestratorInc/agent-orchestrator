/** Find a rendered selection across Markdown spans without changing DOM or selection. */
export function annotationTextRange(root: HTMLElement, text: string): Range | undefined {
	const nodes: Text[] = [];
	const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
	while (walker.nextNode()) nodes.push(walker.currentNode as Text);
	const raw = nodes.map((node) => node.data).join("");
	// Browser selections collapse whitespace differently around rendered Markdown.
	const positions: number[] = [];
	let normalized = "";
	for (let i = 0; i < raw.length; i++) {
		const character = /\s/.test(raw[i]) ? " " : raw[i];
		if (character === " " && normalized.endsWith(" ")) continue;
		normalized += character;
		positions.push(i);
	}
	const selection = text.replace(/\s+/g, " ").trim();
	const index = normalized.indexOf(selection);
	if (!selection || index < 0) return undefined;
	const start = positions[index];
	const end = positions[index + selection.length - 1] + 1;
	const range = document.createRange();
	let offset = 0;
	let started = false;
	for (const node of nodes) {
		const next = offset + node.length;
		if (!started && start < next) {
			range.setStart(node, start - offset);
			started = true;
		}
		if (started && end <= next) {
			range.setEnd(node, end - offset);
			return range;
		}
		offset = next;
	}
	return undefined;
}

export function annotationBody(source: HTMLElement): HTMLElement {
	return source.querySelector<HTMLElement>("[data-chat-message-body]") ?? source;
}

/** Match a source even when Markdown rendering cannot produce an exact Range. */
export function annotationTextMatches(root: HTMLElement, text: string): boolean {
	if (annotationTextRange(root, text)) return true;
	const selected = text.replace(/\s+/g, " ").trim();
	const rendered = (root.textContent ?? "").replace(/\s+/g, " ").trim();
	return Boolean(selected && rendered.includes(selected));
}

/** Returns cleanup so a later jump/unmount cannot clear another highlight. */
export function highlightChatAnnotation(source: HTMLElement, text: string, revision?: number): () => void {
	const body = annotationBody(source);
	const currentRevision = Number(source.dataset.chatMessageRevision);
	const range = revision === undefined || revision === currentRevision ? annotationTextRange(body, text) : undefined;
	const highlightApi = globalThis as unknown as {
		Highlight?: new (...ranges: Range[]) => unknown;
		CSS?: { highlights?: Map<string, unknown> };
	};
	const registry = highlightApi.CSS?.highlights;
	const highlight = range && highlightApi.Highlight ? new highlightApi.Highlight(range) : undefined;
	if (highlight && registry) registry.set("chat-annotation", highlight);
	else body.classList.add("chat-annotation-target");
	return () => {
		if (registry?.get("chat-annotation") === highlight) registry?.delete("chat-annotation");
		body.classList.remove("chat-annotation-target");
	};
}
