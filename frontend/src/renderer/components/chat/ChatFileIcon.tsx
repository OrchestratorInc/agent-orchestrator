import type { ReactNode } from "react";
import { createFileTreeIconResolver, getBuiltInSpriteSheet } from "@pierre/trees";
import { cn } from "../../lib/utils";

// A file named in chat wears the same glyph and colour it has in the Files
// tree: both come from Pierre Trees' complete built-in set.
const resolver = createFileTreeIconResolver({ set: "complete", colored: true });

type Glyph = { viewBox: string; body: string };
let glyphs: Map<string, Glyph> | undefined;

function glyph(name: string): Glyph | undefined {
	if (!glyphs) {
		glyphs = new Map();
		for (const match of getBuiltInSpriteSheet("complete").matchAll(/<symbol id="([^"]+)" viewBox="([^"]+)">([\s\S]*?)<\/symbol>/g)) {
			// No whitespace text nodes, so the icon adds nothing to quoted or found text.
			glyphs.set(match[1], { viewBox: match[2], body: match[3].replace(/>\s+</g, "><").trim() });
		}
	}
	return glyphs.get(name);
}

// The tree paints these inside its shadow root, so its palette is not
// reachable from here. These are its values, light then dark.
const PALETTE = {
	blue: "light-dark(#1a85d4, #69b1ff)",
	cyan: "light-dark(#1ca1c7, #68cdf2)",
	gray: "light-dark(#84848a, #adadb1)",
	green: "light-dark(#199f43, #5ecc71)",
	indigo: "light-dark(#693acf, #9d6afb)",
	mauve: "light-dark(#594c5b, #79697b)",
	orange: "light-dark(#d47628, #ffa359)",
	pink: "light-dark(#d32a61, #ff678d)",
	purple: "light-dark(#a631be, #d568ea)",
	red: "light-dark(#d52c36, #ff6762)",
	teal: "light-dark(#17a5af, #64d1db)",
	vermilion: "light-dark(#ff8c5b, #d5512f)",
	yellow: "light-dark(#d5a910, #ffd452)",
} as const;

const TOKEN_COLOR: Record<string, keyof typeof PALETTE> = {
	astro: "purple", babel: "yellow", bash: "green", biome: "blue", bootstrap: "indigo", browserslist: "yellow",
	bun: "mauve", c: "blue", claude: "orange", cpp: "blue", css: "indigo", database: "purple", docker: "blue",
	eslint: "indigo", git: "vermilion", go: "cyan", graphql: "pink", html: "orange", image: "pink", javascript: "yellow", json: "orange",
	markdown: "green", mcp: "teal", npm: "red", oxc: "cyan", postcss: "red", prettier: "teal", python: "blue", react: "cyan",
	ruby: "red", rust: "orange", sass: "pink", svelte: "red", svg: "orange", svgo: "green", swift: "orange",
	table: "teal", tailwind: "cyan", terraform: "indigo", typescript: "blue", vite: "purple", vscode: "blue",
	vue: "green", wasm: "indigo", webpack: "blue", yml: "red", zig: "orange", zip: "orange",
};

export function ChatFileIcon({ path, className }: { path: string; className?: string }) {
	const icon = resolver.resolveIcon("file-tree-icon-file", path);
	const shape = glyph(icon.name) ?? glyph("file-tree-icon-file");
	if (!shape) return null;
	return (
		<svg
			aria-hidden="true"
			data-file-icon={icon.token ?? "default"}
			viewBox={shape.viewBox}
			className={cn("inline-block size-3.5 shrink-0", className)}
			style={{ color: PALETTE[TOKEN_COLOR[icon.token ?? ""] ?? "gray"] }}
			// Static markup from the icon package's own sprite sheet, never chat text.
			dangerouslySetInnerHTML={{ __html: shape.body }}
		/>
	);
}

/**
 * A workspace file's chip: its Files-tree icon on the accent tint, then `children`
 * as the visible text. Sized like a plain inline-code chip; a `group` ancestor
 * deepens the tint on hover.
 */
export function FileChip({ path, children }: { path: string; children: ReactNode }) {
	return (
		<span className="rounded bg-chat-file py-[2px] pr-[5px] pl-1 font-mono text-xs text-chat-file-foreground transition-colors group-hover:bg-chat-file-hover">
			<ChatFileIcon path={path} className="mr-[5px] align-[-2px]" />
			{children}
		</span>
	);
}
