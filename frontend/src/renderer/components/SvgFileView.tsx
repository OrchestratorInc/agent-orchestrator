import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { IMAGE_CHECKERBOARD, svgDataUrl } from "../lib/image-files";
import { PanelMessage } from "./WorkspaceDiffView";

/** An SVG file's rendered view, beside its code view. */
export function SvgFileView({ content, path }: { content: string; path: string }) {
	const { t } = useTranslation();
	const src = useMemo(() => svgDataUrl(content), [content]);
	const [failedSrc, setFailedSrc] = useState<string | null>(null);
	if (failedSrc === src) return <PanelMessage>{t("files.imagePreviewFailed")}</PanelMessage>;
	return (
		<div className="grid place-items-center p-3 text-passive">
			<img
				alt={path}
				className="max-h-[70vh] max-w-full object-contain"
				onError={() => setFailedSrc(src)}
				src={src}
				style={{ background: IMAGE_CHECKERBOARD }}
			/>
		</div>
	);
}
