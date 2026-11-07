import { File as FileIcon, X } from "lucide-react";
import { useTranslation } from "react-i18next";
import { cn } from "../../lib/utils";
import { Button } from "../ui/button";
import { ChatImage } from "./ChatImage";
import { attachmentName, attachmentURL, IMAGE_ATTACHMENT_PATH } from "./messageAttachments";

/**
 * A message's staged files, as its bubble draws them. The message editor draws
 * the same list, so starting an edit moves nothing.
 */
export function StagedAttachmentItems({
	paths,
	sessionId,
	apiBaseUrl,
	ariaLabel,
	className,
	onRemove,
}: {
	paths: string[];
	sessionId: string;
	apiBaseUrl: string | null;
	ariaLabel: string;
	className?: string;
	/** Offered on files only: an edit always resends the original image data. */
	onRemove?: (path: string) => void;
}) {
	const { t } = useTranslation();
	if (paths.length === 0) return null;
	const images = paths.filter((path) => IMAGE_ATTACHMENT_PATH.test(path));
	return (
		<ul aria-label={ariaLabel} className={cn("flex max-w-full flex-wrap gap-2", className)}>
			{paths.map((path) => {
				const name = attachmentName(path);
				return IMAGE_ATTACHMENT_PATH.test(path) && apiBaseUrl !== null ? (
					<li key={path} className="max-w-full">
						<ChatImage
							src={attachmentURL(apiBaseUrl, sessionId, path)}
							alt={t("chat.image.numbered", { index: images.indexOf(path) + 1 })}
						/>
					</li>
				) : (
					<li
						key={path}
						title={path}
						className="group/attachment flex max-w-full items-center gap-1.5 rounded-md border border-border bg-background px-2 py-1.5 text-xs text-muted-foreground"
					>
						<span className="relative flex size-3.5 shrink-0">
							<FileIcon
								aria-hidden="true"
								className={cn(
									"size-3.5",
									onRemove && "group-focus-within/attachment:opacity-0 group-hover/attachment:opacity-0",
								)}
							/>
							{/* Remove takes the icon's place on hover, so the chip keeps its size. */}
							{onRemove ? (
								<span className="absolute -inset-[5px] flex opacity-0 group-focus-within/attachment:opacity-100 group-hover/attachment:opacity-100">
									<Button
										type="button"
										variant="destructive-ghost"
										size="icon-xs"
										onClick={() => onRemove(path)}
										aria-label={t("chat.attachment.remove", { name })}
									>
										<X aria-hidden="true" className="size-3" />
									</Button>
								</span>
							) : null}
						</span>
						<span className="truncate">{name}</span>
					</li>
				);
			})}
		</ul>
	);
}
