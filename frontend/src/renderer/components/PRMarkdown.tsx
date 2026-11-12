import ReactMarkdown, { type Components } from "react-markdown";
import remarkGfm from "remark-gfm";
import { withoutRawHtml } from "../lib/pr-description";
import { cn } from "../lib/utils";
import { AppLink } from "./AppLink";
import { linksOnlyImages, PRDescriptionImage } from "./PRDescriptionImage";

// The review summary's typography, for PR comments and their preview.
const bodyClass = cn(
	// Comment text reads at full contrast, like a PR page; code stays the size of the words around it.
	"min-w-0 select-text break-words text-xs leading-[1.6] text-foreground/90",
	"[&_a]:font-medium [&_a]:text-foreground [&_a]:underline [&_a]:underline-offset-2",
	"[&_code]:rounded [&_code]:bg-muted/70 [&_code]:px-1 [&_code]:py-px [&_code]:font-mono [&_code]:text-[0.92em] [&_code]:text-foreground [&_code]:[overflow-wrap:anywhere]",
	"[&_h1]:mt-2 [&_h1]:mb-1 [&_h1]:font-semibold [&_h2]:mt-2 [&_h2]:mb-1 [&_h2]:font-semibold [&_h3]:mt-2 [&_h3]:mb-1 [&_h3]:font-semibold",
	"[&_li]:my-0.5 [&_ol]:my-1 [&_ol]:list-decimal [&_ol]:pl-4 [&_p]:my-1.5 [&_pre]:my-1.5 [&_pre]:overflow-x-auto [&_pre]:rounded-md",
	"[&_pre]:border [&_pre]:border-border [&_pre]:bg-muted/40 [&_pre]:p-2 [&_pre_code]:bg-transparent [&_pre_code]:p-0",
	"[&_strong]:font-semibold [&_strong]:text-foreground [&_em]:text-muted-foreground [&_ul]:my-1 [&_ul]:list-disc [&_ul]:pl-4 [&>*:first-child]:mt-0 [&>*:last-child]:mb-0",
	"[&_blockquote]:border-l-2 [&_blockquote]:border-border [&_blockquote]:pl-2 [&_blockquote]:text-muted-foreground",
);

// Outside the component, so a re-render keeps an open image lightbox open.
const markdownComponents: Components = {
	a: ({ node, href, children }) => linksOnlyImages(node) ? <>{children}</> : <AppLink href={href} rel="noopener noreferrer" target="_blank">{children}</AppLink>,
	img: ({ alt, src }) => <PRDescriptionImage alt={alt} src={typeof src === "string" ? src : undefined} />,
};

/**
 * A PR comment's markdown. Provider content: react-markdown never renders raw
 * HTML and would print it as text, so bots' markup (CodeRabbit's comments,
 * <details> and badges) is reduced to its text and images first.
 */
export function PRMarkdown({ body, className }: { body: string; className?: string }) {
	return (
		<div className={cn(bodyClass, className)}>
			<ReactMarkdown components={markdownComponents} remarkPlugins={[remarkGfm]}>{withoutRawHtml(body)}</ReactMarkdown>
		</div>
	);
}
