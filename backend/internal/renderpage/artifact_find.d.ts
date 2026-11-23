export type ArtifactSearchAnchor = Pick<Range, "startContainer" | "startOffset" | "endContainer" | "endOffset"> & { startText: string | null; endText: string | null; row?: string | null };
export interface ArtifactSearchEngine {
  occurrences(source: string, query: string): Array<{ start: number; end: number }>;
  blocks(root: Element | ShadowRoot): Array<{ element: Element; text: string; nodes: Text[] }>;
  range(nodes: Text[], start: number, end: number): Range | undefined;
  ranges(root: Element | ShadowRoot, query: string): Range[];
  anchor(match: Range | undefined): ArtifactSearchAnchor | undefined;
  retained(matches: Range[], previous: ArtifactSearchAnchor | undefined): number;
  paint(matches: Range[], active: number, doc: Document): void;
  clear(): void;
  reveal(match: Range | undefined): void;
}
export function createArtifactSearchEngine(exceptions: Record<string, string>): ArtifactSearchEngine;
