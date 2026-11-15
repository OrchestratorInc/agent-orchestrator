import { useState } from "react";
import { Folder, FolderOpen } from "@/components/app-icons";
import { cn } from "../lib/utils";
import { useUiStore } from "../stores/ui-store";

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
	const avatarsEnabled = useUiStore((state) => state.projectAvatars);
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
					ref={(node) => {
						if (node?.complete && node.naturalWidth > 0 && loadedUrl !== avatarUrl) setLoadedUrl(avatarUrl);
					}}
					src={avatarUrl}
				/>
			) : null}
		</span>
	);
}
