import { FileText, Image as ImageIcon, Tag } from "@/components/app-icons";
import { cn } from "../../lib/utils";
import { ChatImage, ChatImageAttachmentRow } from "./ChatImage";
import { ATTACHMENT_TILE_CLASS } from "./messageAttachments";
import type { ConversationContentSummary } from "../../types/conversation";

interface ConversationContentItem extends ConversationContentSummary {
	data?: string;
}

export function ConversationContentItems({
	content,
	ariaLabel,
	imageLabel,
	imageAlt,
	className,
}: {
	content: ConversationContentItem[];
	ariaLabel: string;
	imageLabel: string;
	imageAlt?: (position: number) => string;
	className?: string;
}) {
	if (content.length === 0) return null;

	let imagePosition = 0;
	return (
		<ChatImageAttachmentRow>
			<ul aria-label={ariaLabel} className={cn("flex max-w-full flex-wrap gap-2", className)}>
				{content.map((item, index) => {
					const previewableImage =
						item.type === "image" &&
						typeof item.data === "string" &&
						item.data.length > 0 &&
						typeof item.mimeType === "string" &&
						item.mimeType.toLowerCase().startsWith("image/");
					const key = `${item.type}-${item.uri ?? item.mimeType ?? item.name ?? "content"}-${index}`;

					if (previewableImage) {
						imagePosition += 1;
						return (
							<li key={key} className="max-w-full">
								<ChatImage
									src={`data:${item.mimeType};base64,${item.data}`}
									alt={imageAlt?.(imagePosition) ?? `${imageLabel} ${imagePosition}`}
								/>
							</li>
						);
					}

					const Icon = item.type === "image" ? ImageIcon : item.type === "resource" ? FileText : Tag;
					const label = item.type === "image" ? item.mimeType || imageLabel : item.name || item.uri || item.type;
					return (
						<li key={key} title={item.uri || label} className={ATTACHMENT_TILE_CLASS}>
							<span className="line-clamp-2 text-foreground">{label}</span>
							<Icon aria-hidden="true" className="size-3.5 shrink-0" />
						</li>
					);
				})}
			</ul>
		</ChatImageAttachmentRow>
	);
}
