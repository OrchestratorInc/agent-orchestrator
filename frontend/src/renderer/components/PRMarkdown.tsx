import ReactMarkdown, { type Components } from "react-markdown";
import remarkGfm from "remark-gfm";
import { cn } from "../lib/utils";
import { AppLink } from "./AppLink";
import { PRDescriptionImage } from "./PRDescriptionImage";

// The review summary's typography, for PR comments and their preview.
const bodyClass = cn(
	"min-w-0 select-text break-words text-xs leading-relaxed text-muted-foreground",
	"[&_a]:font-medium [&_a]:text-foreground [&_a]:underline [&_a]:underline-offset-2",
	"[&_code]:rounded [&_code]:bg-muted/55 [&_code]:px-1 [&_code]:py-0.5 [&_code]:font-mono [&_code]:text-foreground",
	"[&_li]:my-0.5 [&_ol]:my-1 [&_ol]:list-decimal [&_ol]:pl-4 [&_p]:my-1 [&_pre]:my-1.5 [&_pre]:overflow-x-auto [&_pre]:rounded-md",
	"[&_pre]:border [&_pre]:border-border [&_pre]:bg-muted/35 [&_pre]:p-2 [&_pre_code]:bg-transparent [&_pre_code]:p-0",
	"[&_strong]:text-foreground [&_ul]:my-1 [&_ul]:list-disc [&_ul]:pl-4 [&>*:first-child]:mt-0 [&>*:last-child]:mb-0",
	"[&_blockquote]:border-l-2 [&_blockquote]:border-border [&_blockquote]:pl-2",
);

// Outside the component, so a re-render keeps an open image lightbox open.
const markdownComponents: Components = {
	a: ({ href, children }) => <AppLink href={href} rel="noopener noreferrer" target="_blank">{children}</AppLink>,
	img: ({ alt, src }) => <PRDescriptionImage alt={alt} src={typeof src === "string" ? src : undefined} />,
};

/** A PR comment's markdown. Provider content: react-markdown never renders raw HTML. */
export function PRMarkdown({ body, className }: { body: string; className?: string }) {
	return (
		<div className={cn(bodyClass, className)}>
			<ReactMarkdown components={markdownComponents} remarkPlugins={[remarkGfm]}>{body}</ReactMarkdown>
		</div>
	);
}
