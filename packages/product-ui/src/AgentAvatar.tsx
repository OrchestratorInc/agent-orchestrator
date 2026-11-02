import type { HTMLAttributes, ImgHTMLAttributes } from "react";
import { getAgentIdentity } from "./agents";
import { cn } from "./utils";

/** A single mark that reads on both themes, or a pair the active theme picks from. */
export type AgentLogo = string | { readonly light: string; readonly dark: string };

export type AgentLogoSources = Readonly<Record<string, AgentLogo | undefined>>;

export type AgentAvatarProps = {
	provider: string;
	className?: string;
	decorative?: boolean;
	logoSrc?: AgentLogo;
	logoSources?: AgentLogoSources;
};

export function AgentAvatar({
	provider,
	className,
	decorative = false,
	logoSrc,
	logoSources,
}: AgentAvatarProps) {
	const identity = getAgentIdentity(provider);
	const logo = logoSrc ?? (identity.logoKey ? logoSources?.[identity.logoKey] : undefined);
	if (logo && typeof logo !== "string") {
		// Both marks stay mounted so a theme switch is a pure CSS swap; the wrapper
		// carries the accessible name so only one image is ever announced.
		const pairAccessibility: Pick<HTMLAttributes<HTMLSpanElement>, "aria-hidden" | "aria-label" | "role" | "title"> =
			decorative
				? { "aria-hidden": true }
				: { "aria-label": provider, role: "img", title: provider };
		return (
			<span {...pairAccessibility} className={cn("inline-flex size-icon-xl shrink-0", className)}>
				<img alt="" className="size-full object-contain dark:hidden" draggable={false} src={logo.light} />
				<img alt="" className="hidden size-full object-contain dark:block" draggable={false} src={logo.dark} />
			</span>
		);
	}
	if (logo) {
		const imageAccessibility: Pick<ImgHTMLAttributes<HTMLImageElement>, "alt" | "aria-hidden" | "title"> =
			decorative
				? { alt: "", "aria-hidden": true }
				: { alt: provider, title: provider };
		return (
			<img
				{...imageAccessibility}
				className={cn("size-icon-xl shrink-0 object-contain", className)}
				draggable={false}
				src={logo}
			/>
		);
	}

	const fallbackAccessibility: Pick<HTMLAttributes<HTMLSpanElement>, "aria-hidden" | "aria-label" | "role" | "title"> =
		decorative
			? { "aria-hidden": true }
			: { "aria-label": provider, role: "img", title: provider };
	return (
		<span
			{...fallbackAccessibility}
			className={cn(
				"inline-flex size-icon-xl shrink-0 items-center justify-center text-caption font-bold uppercase leading-none text-muted-foreground",
				className,
			)}
		>
			{identity.initial}
		</span>
	);
}
