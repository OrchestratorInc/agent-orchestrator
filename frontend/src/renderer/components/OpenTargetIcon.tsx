import { Code2, FolderOpen, SquareTerminal } from "lucide-react";
import { useState } from "react";
import type { OpenTarget } from "../../shared/editor-handoff";
import {
	AndroidStudioIcon,
	AntigravityIcon,
	CursorIcon,
	FinderIcon,
	GhosttyIcon,
	JetBrainsIcon,
	SublimeIcon,
	VSCodeIcon,
	VSCodiumIcon,
	WindsurfIcon,
	ZedIcon,
} from "./icons";

const editorIcons: Record<string, typeof VSCodeIcon> = {
	vscode: VSCodeIcon,
	"vscode-insiders": VSCodeIcon,
	vscodium: VSCodiumIcon,
	cursor: CursorIcon,
	windsurf: WindsurfIcon,
	zed: ZedIcon,
	antigravity: AntigravityIcon,
	sublime: SublimeIcon,
	"android-studio": AndroidStudioIcon,
	intellij: JetBrainsIcon,
	webstorm: JetBrainsIcon,
	pycharm: JetBrainsIcon,
	goland: JetBrainsIcon,
	phpstorm: JetBrainsIcon,
	rubymine: JetBrainsIcon,
	clion: JetBrainsIcon,
	rider: JetBrainsIcon,
	fleet: JetBrainsIcon,
};

const editorColors: Record<string, string> = {
	vscode: "#1F9CF0",
	"vscode-insiders": "#1F9CF0",
	vscodium: "#2F80ED",
	antigravity: "#4285F4",
	sublime: "#FF9800",
	"android-studio": "#3DDC84",
};

export function OpenTargetIcon({ target, className }: { target?: OpenTarget; className?: string }) {
	const [failedIcon, setFailedIcon] = useState<string>();
	if (target?.kind === "file_manager") {
		const Icon = target.name === "Finder" ? FinderIcon : FolderOpen;
		return <Icon className={className} aria-hidden="true" />;
	}
	if (target?.id === "terminal") return <SquareTerminal className={className} aria-hidden="true" />;
	if (target?.id === "ghostty") return <GhosttyIcon className={className} aria-hidden="true" />;
	// Preserve AO’s established symbols; newly discovered apps use their OS icon.
	const BrandedIcon = target && editorIcons[target.id];
	if (BrandedIcon) {
		const color = editorColors[target.id];
		return <BrandedIcon className={className} style={color ? { color } : undefined} aria-hidden="true" />;
	}
	const iconDataUrl = target?.iconDataUrl;
	if (iconDataUrl && /^data:image\/png;base64,[A-Za-z0-9+/]+={0,2}$/.test(iconDataUrl) && iconDataUrl !== failedIcon) {
		// Use the same SVG slot and sizing rules as every other menu icon.
		return (
			<svg width="24" height="24" viewBox="0 0 24 24" className={className} aria-hidden="true">
				<image href={iconDataUrl} width="24" height="24" onError={() => setFailedIcon(iconDataUrl)} />
			</svg>
		);
	}
	if (target?.kind === "terminal") {
		return <SquareTerminal className={className} aria-hidden="true" />;
	}
	return <Code2 className={className} aria-hidden="true" />;
}

