import { createContext, useContext, type ReactNode } from "react";
import { cn } from "../../lib/utils";

export const SettingsPageTitleContext = createContext<string | null>(null);

function repeatsPageTitle(title: string, pageTitle: string | null) {
	if (!pageTitle) return false;
	return title.trim().toLocaleLowerCase() === pageTitle.trim().toLocaleLowerCase();
}

export function SettingsSection({
	title,
	sectionId,
	titleHidden,
	description,
	grouped,
	toolbar,
	action,
	titleAction,
	testId,
	children,
}: {
	title: string;
	sectionId?: string;
	titleHidden?: boolean;
	description?: ReactNode;
	grouped?: boolean;
	toolbar?: ReactNode;
	action?: ReactNode;
	titleAction?: ReactNode;
	testId?: string;
	children: ReactNode;
}) {
	const pageTitle = useContext(SettingsPageTitleContext);
	const showTitle = !titleHidden && !repeatsPageTitle(title, pageTitle);
	const heading = <h2 className="min-w-0 text-sm font-medium leading-5 text-foreground">{title}</h2>;
	const body = (
		<div
			className={cn(
				"w-full",
				grouped
					? "settings-grouped-rows flex w-full flex-col"
					: "flex w-full flex-col gap-1.5",
			)}
		>
			{children}
		</div>
	);
	return (
		<section
			className="flex w-full flex-col items-stretch gap-(--size-settings-section-inner-gap)"
			data-testid={testId ?? (sectionId ? "settings-section" : undefined)}
			data-section={sectionId}
		>
			{action || (showTitle && titleAction) ? (
				<div className="flex h-5 items-center justify-between gap-3 px-(--settings-row-inset)">
					{!showTitle ? <span /> : titleAction ? (
						<div className="flex min-w-0 items-center gap-0.5">
							{heading}
							{titleAction}
						</div>
					) : heading}
					{action ? <div className="flex shrink-0 items-center">{action}</div> : null}
				</div>
			) : showTitle ? (
				<h2 className="px-(--settings-row-inset) text-sm font-medium leading-5 text-foreground">{title}</h2>
			) : null}
			{showTitle && description ? (
				<p className="-mt-2 px-(--settings-row-inset) text-pretty text-control leading-4 text-settings-muted">{description}</p>
			) : null}
			{toolbar !== undefined ? (
				<div className="flex w-full flex-col">
					{toolbar}
					{body}
				</div>
			) : body}
		</section>
	);
}
