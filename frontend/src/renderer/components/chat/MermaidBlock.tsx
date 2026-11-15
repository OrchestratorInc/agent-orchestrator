import { memo, useCallback, useEffect, useState, type MouseEvent } from "react";
import { cn } from "../../lib/utils";
import { Pressable } from "../ui/pressable";
import { isWebLink, openLinkInSystemBrowser } from "../../lib/external-link-policy";
import { isRenderableDiagram, renderMermaidDiagram, type DiagramTheme } from "../../lib/mermaid-diagram";
import { CopyButton } from "./CopyButton";

function readTheme(): DiagramTheme {
	if (typeof document !== "undefined") {
		const stored = document.documentElement.dataset.theme;
		if (stored === "light" || stored === "dark") return stored;
	}
	if (typeof window !== "undefined" && typeof window.matchMedia === "function") {
		return window.matchMedia("(prefers-color-scheme: light)").matches ? "light" : "dark";
	}
	return "dark";
}

function useDiagramTheme(): DiagramTheme {
	const [theme, setTheme] = useState<DiagramTheme>(readTheme);

	useEffect(() => {
		const refresh = () => setTheme(readTheme());
		const observer = new MutationObserver(refresh);
		observer.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] });
		const media = window.matchMedia("(prefers-color-scheme: light)");
		media.addEventListener("change", refresh);
		return () => {
			observer.disconnect();
			media.removeEventListener("change", refresh);
		};
	}, []);

	return theme;
}

export const MermaidBlock = memo(function MermaidBlock({
	code,
	streaming = false,
	interactive = true,
	onLinkOpen,
}: {
	code: string;
	streaming?: boolean;
	interactive?: boolean;
	onLinkOpen?: (url: string) => void;
}) {
	const theme = useDiagramTheme();
	const [svg, setSvg] = useState<string | undefined>(undefined);
	const [failed, setFailed] = useState(false);
	const [showSource, setShowSource] = useState(false);

	useEffect(() => {
		if (streaming || !isRenderableDiagram(code)) {
			setSvg(undefined);
			setFailed(!streaming && !isRenderableDiagram(code) && code.trim().length > 0);
			return;
		}
		let live = true;
		setSvg(undefined);
		setFailed(false);
		void renderMermaidDiagram(code, theme).then(
			(diagram) => {
				if (live) setSvg(diagram);
			},
			() => {
				if (live) setFailed(true);
			},
		);
		return () => {
			live = false;
		};
	}, [code, theme, streaming]);

	const onDiagramClick = useCallback(
		(event: MouseEvent<HTMLDivElement>) => {
			const anchor = (event.target as Element).closest?.("a[href]");
			if (!anchor) return;
			const href = anchor.getAttribute("href");
			if (!href) return;
			event.preventDefault();
			const toSystemBrowser =
				event.metaKey || event.ctrlKey || event.altKey || !onLinkOpen || !isWebLink(href);
			if (toSystemBrowser) {
				void openLinkInSystemBrowser(href);
				return;
			}
			onLinkOpen(href);
		},
		[onLinkOpen],
	);

	const onDiagramAuxClick = useCallback(
		(event: MouseEvent<HTMLDivElement>) => {
			if (event.button !== 1) return;
			onDiagramClick(event);
		},
		[onDiagramClick],
	);

	const showingDiagram = svg !== undefined && !showSource;

	return (
		<div data-chat-find-exclude="" className="chat-code group/code my-2.5 overflow-hidden rounded-lg border border-border bg-surface">
			<div data-excerpt-exclude="" className="flex items-center gap-2 border-b border-border bg-raised/40 px-2.5 py-1">
				<span className="text-caption text-muted-foreground">
					mermaid
				</span>
				{interactive ? <div className="ml-auto flex items-center gap-1.5">
					{svg !== undefined ? (
						<div
							role="group"
							aria-label="Diagram view"
							className="flex items-center rounded-md border border-border bg-background p-0.5"
						>
							<Pressable
								onClick={() => setShowSource(false)}
								aria-pressed={!showSource}
								className={cn(
									"rounded px-2 py-0.5 text-xs transition-colors",
									showSource
										? "text-muted-foreground hover:text-foreground"
										: "bg-raised text-foreground",
								)}
							>
								Diagram
							</Pressable>
							<Pressable
								onClick={() => setShowSource(true)}
								aria-pressed={showSource}
								className={cn(
									"rounded px-2 py-0.5 text-xs transition-colors",
									showSource
										? "bg-raised text-foreground"
										: "text-muted-foreground hover:text-foreground",
								)}
							>
								Code
							</Pressable>
						</div>
					) : null}
					<div className="flex items-center gap-0.5 opacity-0 transition-opacity duration-150 focus-within:opacity-100 group-hover/code:opacity-100">
						<CopyButton text={code} label="Copy diagram source" />
					</div>
				</div> : null}
			</div>
			{showingDiagram ? (
				<div className="overflow-x-auto bg-background px-3 py-3">
					<div
						data-testid="mermaid-diagram"
						role="img"
						aria-label="Mermaid diagram"
						onClick={interactive ? onDiagramClick : undefined}
						onAuxClick={interactive ? onDiagramAuxClick : undefined}
						className="mermaid-diagram mx-auto max-w-full [&>svg]:mx-auto [&>svg]:h-auto [&>svg]:max-w-full"
						dangerouslySetInnerHTML={{ __html: svg }}
					/>
				</div>
			) : (
				<pre className="scrollbar-none overflow-x-auto px-3 py-2.5">
					<code className="font-mono text-xs leading-[1.6] text-foreground">{code}</code>
				</pre>
			)}
			{failed && !showSource ? (
				<div data-excerpt-exclude="" className="border-t border-border px-3 py-1.5 text-caption text-muted-foreground">
					Couldn&apos;t render this diagram — showing its source.
				</div>
			) : null}
		</div>
	);
});
