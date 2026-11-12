import { useState } from "react";
import { Folder, FolderOpen } from "@/components/app-icons";
import { isNewSidebarDesign } from "../lib/new-sidebar-design";
import { cn } from "../lib/utils";
import { useUiStore } from "../stores/ui-store";

// Tile colours for projects without an avatar. Picked by a hash of the
// project path, so a project keeps its colour across launches.
const PROJECT_LETTER_TILE_COLORS = [
	"bg-red-400/20",
	"bg-orange-400/20",
	"bg-amber-400/20",
	"bg-lime-400/20",
	"bg-emerald-400/20",
	"bg-teal-400/20",
	"bg-sky-400/20",
	"bg-blue-400/20",
	"bg-indigo-400/20",
	"bg-violet-400/20",
	"bg-fuchsia-400/20",
	"bg-pink-400/20",
] as const;

function projectTileColor(seed: string): string {
	let hash = 0;
	for (let index = 0; index < seed.length; index++) hash = (hash * 31 + seed.charCodeAt(index)) | 0;
	return PROJECT_LETTER_TILE_COLORS[Math.abs(hash) % PROJECT_LETTER_TILE_COLORS.length];
}

/** A project's icon. With repository avatars on: the owner avatar as a 4px-radius
 *  squircle, or a colour-coded first-letter tile while it loads and when there
 *  is no avatar or it fails. With them off: a folder. */
export function ProjectIcon({
	avatarUrl,
	name,
	seed,
	expanded,
	className,
}: {
	avatarUrl?: string;
	name: string;
	seed: string;
	expanded: boolean;
	className?: string;
}) {
	const avatarsEnabled = useUiStore((state) => state.projectAvatars && isNewSidebarDesign(state));
	const [loadedUrl, setLoadedUrl] = useState<string>();
	const [failedUrl, setFailedUrl] = useState<string>();
	if (!avatarsEnabled) {
		const Icon = expanded ? FolderOpen : Folder;
		return <Icon className={className} />;
	}
	const showAvatar = avatarUrl !== undefined && failedUrl !== avatarUrl;
	const loaded = showAvatar && loadedUrl === avatarUrl;
	const letter = name.match(/[\p{L}\p{N}]/u)?.[0]?.toUpperCase() ?? "?";
	return (
		<span className={cn("relative inline-flex shrink-0", className ?? "size-icon-md")}>
			{loaded ? null : (
				<span
					className={cn(
						"inline-flex size-full items-center justify-center rounded-[4px] font-semibold leading-none text-foreground/80",
						className ? "text-xs" : "text-[9px]",
						projectTileColor(seed),
					)}
				>
					{letter}
				</span>
			)}
			{showAvatar ? (
				<img
					alt=""
					className={cn("absolute inset-0 size-full rounded-[4px] object-cover", !loaded && "invisible")}
					draggable={false}
					onError={() => setFailedUrl(avatarUrl)}
					onLoad={() => setLoadedUrl(avatarUrl)}
					// A cached image can finish before React attaches onLoad.
					ref={(node) => {
						if (node?.complete && node.naturalWidth > 0 && loadedUrl !== avatarUrl) setLoadedUrl(avatarUrl);
					}}
					src={avatarUrl}
				/>
			) : null}
		</span>
	);
}
