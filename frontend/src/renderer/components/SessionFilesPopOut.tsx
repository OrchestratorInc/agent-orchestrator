import { useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { usePopOutDialog } from "../hooks/usePopOutDialog";
import { useWindowFullScreen } from "../hooks/useWindowFullScreen";
import { hidesShellTopbar } from "../lib/platform";
import { cn } from "../lib/utils";

export function SessionFilesPopOut({ children, onClose }: { children: (topbarHost: HTMLDivElement) => ReactNode; onClose?: () => void }) {
	const { t } = useTranslation();
	const [topbarHost, setTopbarHost] = useState<HTMLDivElement | null>(null);
	const isNativeFullScreen = useWindowFullScreen();
	const macWindowed = hidesShellTopbar() && !isNativeFullScreen;
	const dialogProps = usePopOutDialog<HTMLDivElement>(onClose);
	return <div {...dialogProps} aria-label={t("files.maximize")} className={cn("files-popout-overlay outline-none", macWindowed && "files-popout-overlay--mac-windowed")}>
		<div aria-hidden="true" className="files-popout-backdrop" />
		<div className={cn("files-popout-titlebar", macWindowed && "files-popout-titlebar--mac-windowed")} data-testid="files-popout-topbar" ref={setTopbarHost} />
		<div className="files-popout-frame">{topbarHost ? children(topbarHost) : null}</div>
	</div>;
}
