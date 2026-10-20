import { createContext, useContext, type ReactNode } from "react";
import { cn } from "../../lib/utils";

/**
 * Title of the settings page the section renders on (null outside a single
 * page). A section heading equal to it ("Harness" on Harness) is hidden; any
 * other heading shows. A heading that only rewords the title ("Keyboard
 * shortcuts" on Shortcuts) passes `titleHidden` at its call site.
 */
export const SettingsPageTitleContext = createContext<string | null>(null);

function repeatsPageTitle(title: string, pageTitle: string | null) {
	if (!pageTitle) return false;
	return title.trim().toLocaleLowerCase() === pageTitle.trim().toLocaleLowerCase();
}

/**
 * Figma section: column, gap 12px between heading and rows.
 * `sectionId` (optional) marks the section for the renderer smoke suite as
 * [data-testid="settings-section"][data-section=<id>].
 * `grouped` puts the children in one card (.settings-grouped-rows): direct
 * children are SettingsRow or `.settings-content-row` blocks, separated by space;
 * the card outline is the only border.
 * `toolbar` sits under the heading and above the card — a SettingsSearch, for
 * instance. Its sticky wrapper's bottom padding is the gap to the card.
 * `action` sits at the right end of the heading line, vertically centred: a
 * ghost "Copy report" button or a muted figure, never a second heading.
 * `testId` replaces the section's data-testid for suites that are not the
 * settings smoke suite.
 */
export function SettingsSection({
	title,
	sectionId,
	titleHidden,
	grouped,
	toolbar,
	action,
	testId,
	children,
}: {
	title: string;
	sectionId?: string;
	titleHidden?: boolean;
	grouped?: boolean;
	toolbar?: ReactNode;
	action?: ReactNode;
	testId?: string;
	children: ReactNode;
}) {
	const pageTitle = useContext(SettingsPageTitleContext);
	const showTitle = !titleHidden && !repeatsPageTitle(title, pageTitle);
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
			{action ? (
				// h-5, the heading's own line: a taller action (a ghost button)
				// centres on it and overhangs, so heading→card stays 12px.
				<div className="flex h-5 items-center justify-between gap-3 px-(--settings-row-inset)">
					{showTitle ? <h2 className="min-w-0 text-sm font-medium leading-5 text-foreground">{title}</h2> : <span />}
					<div className="flex shrink-0 items-center">{action}</div>
				</div>
			) : showTitle ? (
				<h2 className="px-(--settings-row-inset) text-sm font-medium leading-5 text-foreground">{title}</h2>
			) : null}
			{toolbar !== undefined ? (
				// The wrapper stays whenever the prop is passed, even as null, so a toolbar
				// that comes and goes does not remount the body. A search-first page (no
				// visible title) tucks up under the page heading.
				<div className={cn("flex w-full flex-col", toolbar && !showTitle && "-mt-6")}>
					{toolbar}
					{body}
				</div>
			) : body}
		</section>
	);
}
