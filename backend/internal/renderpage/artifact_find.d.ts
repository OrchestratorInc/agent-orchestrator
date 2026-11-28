export type ArtifactSearchAnchor = Pick<Range, "startContainer" | "startOffset" | "endContainer" | "endOffset"> & { startText: string | null; endText: string | null; row?: string | null };
/** One occurrence may span slots in different DOM roots. */
export type ArtifactSearchMatch = Range[];
export interface ArtifactSearchEngine {
  occurrences(source: string, query: string): Array<{ start: number; end: number }>;
  occurrencesAsync(source: string, query: string, signal?: AbortSignal): Promise<Array<{ start: number; end: number }>>;
  blocks(root: Element | ShadowRoot): Array<{ element: Element; text: string; nodes: Text[] }>;
  range(nodes: Text[], start: number, end: number): Range | undefined;
  ranges(root: Element | ShadowRoot, query: string): Range[];
  search(root: Element | ShadowRoot, query: string, signal?: AbortSignal): Promise<ArtifactSearchMatch[]>;
  anchor(match: Range | ArtifactSearchMatch | undefined): ArtifactSearchAnchor | undefined;
  retained(matches: Range[] | ArtifactSearchMatch[], previous: ArtifactSearchAnchor | undefined): number;
  paint(matches: Range[] | ArtifactSearchMatch[], active: number, doc: Document): void;
  clear(): void;
  reveal(match: Range | ArtifactSearchMatch | undefined): void;
  observe(root: Element | ShadowRoot, changed: () => void): () => void;
}
export function createArtifactSearchEngine(exceptions: Record<string, string>): ArtifactSearchEngine;
