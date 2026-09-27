import { useMemo } from "react";
import Markdown, { type Components } from "react-markdown";
import { prepareDesktopReleaseNotes } from "../lib/desktop-release-notes";
import { ProductExternalLink } from "./ProductExternalLink";

const AO_RELEASE_LINK_PATTERN =
	/^https:\/\/github\.com\/Untrivial-ai\/agent-orchestrator\/(?:pull\/\d+|commit\/[0-9a-f]{7,40}|compare\/v\d+\.\d+\.\d+(?:-nightly\.\d{12})?\.\.\.v\d+\.\d+\.\d+(?:-nightly\.\d{12})?)$/i;

const releaseNoteComponents: Components = {
	h3: ({ children }) => (
		<h3 className="mt-4 text-sm font-semibold text-foreground first:mt-0">{children}</h3>
	),
	p: ({ children }) => <p className="mt-2 first:mt-0">{children}</p>,
	ul: ({ children }) => <ul className="mt-2 list-disc space-y-1 pl-5">{children}</ul>,
	ol: ({ children }) => <ol className="mt-2 list-decimal space-y-1 pl-5">{children}</ol>,
	blockquote: ({ children }) => (
		<blockquote className="mt-3 border-l-2 border-settings-muted pl-3 text-settings-muted">
			{children}
		</blockquote>
	),
	code: ({ children }) => <code className="font-mono text-[0.92em]">{children}</code>,
	a: ({ href, children }) => href ? (
		<ProductExternalLink
			href={href}
			className="underline decoration-settings-muted underline-offset-2 transition-colors hover:text-foreground"
		>
			{children}
		</ProductExternalLink>
	) : <>{children}</>,
};

function releaseNoteUrl(url: string): string {
	return AO_RELEASE_LINK_PATTERN.test(url) ? url : "";
}

export function DesktopReleaseNotes({ notes, textClassName }: {
	notes: string;
	textClassName: string;
}) {
	const prepared = useMemo(() => prepareDesktopReleaseNotes(notes), [notes]);

	if (!prepared) return null;

	return (
		<div className={textClassName}>
			<Markdown components={releaseNoteComponents} urlTransform={releaseNoteUrl}>
				{prepared}
			</Markdown>
		</div>
	);
}
