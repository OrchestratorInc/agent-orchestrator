import type { ReactNode, SVGProps } from "react";
import { cn } from "./utils";

type IconProps = SVGProps<SVGSVGElement>;

// Myna UI drawings (MIT, @mynaui/icons-react) inlined so this package needs
// no icon dependency. Bot, FileCode and the closed/draft PR glyphs have no Myna
// counterpart and keep their lucide drawings at Myna's 1.5 stroke.
//
// Myna draws inside an 18-unit frame (3-21) while lucide uses 20 (2-22), so a
// Myna glyph reads about 10% smaller at the same CSS size. `frame: "crop"`
// (the default) crops the viewBox to Myna's frame and thins the stroke by the
// same ratio so both sets render at one optical size. Drawings that already
// match lucide's size (chevrons, X, the lucide fallbacks) keep the full frame:
// cropping them would oversize them, and it moves their integer coordinates
// onto fractional device pixels, which rasterizes unstably under transforms.
const CROP_VIEWBOX = "1.2 1.2 21.6 21.6";
const CROP_STROKE = "1.35";

function Icon({
	children,
	className,
	frame = "crop",
	name,
	...props
}: IconProps & {
	children: ReactNode;
	frame?: "crop" | "full";
	name: string;
}) {
	return (
		<svg
			aria-hidden="true"
			className={cn("icon", `icon-${name}`, className)}
			fill="none"
			height="24"
			stroke="currentColor"
			strokeLinecap="round"
			strokeLinejoin="round"
			strokeWidth={frame === "crop" ? CROP_STROKE : "1.5"}
			viewBox={frame === "crop" ? CROP_VIEWBOX : "0 0 24 24"}
			width="24"
			xmlns="http://www.w3.org/2000/svg"
			{...props}
		>
			{children}
		</svg>
	);
}

export function ArrowUpRightIcon(props: IconProps) {
	return (
		<Icon frame="full" name="arrow-up-right" {...props}>
			<path d="m6.5 17.5 11-11m0 9v-9h-9" />
		</Icon>
	);
}

export function ChevronIcon({
	direction,
	...props
}: IconProps & {
	direction: "down" | "right" | "up";
}) {
	return (
		<Icon frame="full" name={`chevron-${direction}`} {...props}>
			{direction === "down" ? (
				<path d="m6 9 6 6 6-6" />
			) : direction === "up" ? (
				<path d="m18 15-6-6-6 6" />
			) : (
				<path d="m9 18 6-6-6-6" />
			)}
		</Icon>
	);
}

export function BotIcon(props: IconProps) {
	return (
		<Icon frame="full" name="bot" {...props}>
			<path d="M12 8V4H8" />
			<rect width="16" height="12" x="4" y="8" rx="2" />
			<path d="M2 14h2" />
			<path d="M20 14h2" />
			<path d="M9 13v2" />
			<path d="M15 13v2" />
		</Icon>
	);
}

export function CheckIcon(props: IconProps) {
	return (
		<Icon frame="full" name="check" {...props}>
			<path d="M20 6 9 17l-5-5" />
		</Icon>
	);
}

export function FileCodeIcon(props: IconProps) {
	return (
		<Icon frame="full" name="file-code-2" {...props}>
			<path d="M14.5 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7.5L14.5 2z" />
			<polyline points="14 2 14 8 20 8" />
			<path d="m10 13-2 2 2 2" />
			<path d="m14 17 2-2-2-2" />
		</Icon>
	);
}

export function PlusIcon(props: IconProps) {
	return (
		<Icon name="plus" {...props}>
			<path d="M18 12h-6m0 0H6m6 0V6m0 6v6" />
		</Icon>
	);
}

export function FileTextIcon(props: IconProps) {
	return (
		<Icon name="file-text" {...props}>
			<path d="M9.478 3H7.25A2.25 2.25 0 0 0 5 5.25v13.5A2.25 2.25 0 0 0 7.25 21h9a2.25 2.25 0 0 0 2.25-2.25V12M9.478 3c1.243 0 2.272 1.007 2.272 2.25V7.5A2.25 2.25 0 0 0 14 9.75h2.25A2.25 2.25 0 0 1 18.5 12M9.478 3c3.69 0 9.022 5.36 9.022 9M9 16.5h6m-6-3h4" />
		</Icon>
	);
}

export function GitBranchIcon(props: IconProps) {
	return (
		<Icon name="git-branch" {...props}>
			<path d="M6 15a3 3 0 1 1 0 6 3 3 0 0 1 0-6" />
			<path d="M18 9a3 3 0 1 1 0-6 3 3 0 0 1 0 6m0 0a9 9 0 0 1-9 9m-3-3V3" />
		</Icon>
	);
}

export function GitPullRequestIcon(props: IconProps) {
	return (
		<Icon name="git-pull-request" {...props}>
			<path d="M6 9a3 3 0 1 0 0-6 3 3 0 0 0 0 6m12 12a3 3 0 1 0 0-6 3 3 0 0 0 0 6M6 9v12m12-6V7.5a2 2 0 0 0-2-2h-2.5" />
			<path d="M14.5 8 12 5.5 14.5 3" />
		</Icon>
	);
}

export function GitMergeIcon(props: IconProps) {
	return (
		<Icon name="git-merge" {...props}>
			<path d="M6 9a3 3 0 1 0 0-6 3 3 0 0 0 0 6m9 9a3 3 0 1 0 6 0 3 3 0 0 0-6 0m0 0a9 9 0 0 1-9-9m0 0v12" />
		</Icon>
	);
}

export function GitPullRequestClosedIcon(props: IconProps) {
	return (
		<Icon frame="full" name="git-pull-request-closed" {...props}>
			<circle cx="6" cy="6" r="3" />
			<path d="M6 9v12" />
			<path d="m15 9 6 6" />
			<path d="m21 9-6 6" />
		</Icon>
	);
}

export function GitPullRequestDraftIcon(props: IconProps) {
	return (
		<Icon frame="full" name="git-pull-request-draft" {...props}>
			<circle cx="6" cy="6" r="3" />
			<path d="M6 9v12" />
			<path d="M18 6h.01" />
			<path d="M18 10v8" />
		</Icon>
	);
}

export function LoaderCircleIcon(props: IconProps) {
	return (
		<Icon name="loader-circle" {...props}>
			<path d="M12 3v3m6.366-.366-2.12 2.12M21 12h-3m.366 6.366-2.12-2.12M12 21v-3m-6.366.366 2.12-2.12M3 12h3m-.366-6.366 2.12 2.12" />
		</Icon>
	);
}

export function MessageSquareIcon(props: IconProps) {
	return (
		<Icon frame="full" name="message-square" {...props}>
			<path d="M3.464 16.828C2 15.657 2 14.771 2 11s0-5.657 1.464-6.828C4.93 3 7.286 3 12 3s7.071 0 8.535 1.172S22 7.229 22 11s0 4.657-1.465 5.828C19.072 18 16.714 18 12 18c-2.51 0-3.8 1.738-6 3v-3.212c-1.094-.163-1.899-.45-2.536-.96" />
		</Icon>
	);
}

export function MoreHorizontalIcon(props: IconProps) {
	return (
		<Icon frame="full" name="more-horizontal" {...props}>
			<circle cx="12" cy="12" r="1" />
			<circle cx="19" cy="12" r="1" />
			<circle cx="5" cy="12" r="1" />
		</Icon>
	);
}

export function PaperclipIcon(props: IconProps) {
	return (
		<Icon name="paperclip" {...props}>
			<path d="m3.845 10.224 5.739-5.53c5.429-5.237 14.246 2.843 8.596 8.294l-7.112 6.862c-3.684 3.554-9.667-1.929-5.833-5.628l7.01-6.763c1.939-1.87 5.087 1.015 3.07 2.962L9.492 16" />
		</Icon>
	);
}

export function XIcon(props: IconProps) {
	return (
		<Icon frame="full" name="x" {...props}>
			<path d="M18 6 6 18M6 6l12 12" />
		</Icon>
	);
}

/** A check's state as a shape, so colour never carries it alone (lucide drawings). */
export function CheckStateIcon({
	state,
	...props
}: IconProps & {
	state: "failed" | "running" | "pending" | "passed" | "skipped";
}) {
	return (
		<Icon frame="full" name={`check-${state}`} strokeWidth="2" {...props}>
			{state === "running" ? (
				<>
					<circle cx="12" cy="12" opacity="0.3" r="10" />
					<path d="M22 12A10 10 0 0 0 12 2" />
				</>
			) : (
				<circle cx="12" cy="12" r="10" />
			)}
			{state === "passed" ? <path d="m8.5 12.5 2.5 2.5 4.5-5" /> : null}
			{state === "failed" ? <path d="m15 9-6 6m0-6 6 6" /> : null}
			{state === "pending" ? <path d="M12 7v5l3 2" /> : null}
			{state === "skipped" ? <path d="m8.5 15.5 7-7" /> : null}
		</Icon>
	);
}
