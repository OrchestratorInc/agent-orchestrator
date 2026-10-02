import { useEffect, useState } from "react";
import type { ReactNode } from "react";

export type AttachmentPreviewResolver = (id: string) => Promise<string>;
export function AttachmentPreview({
	id,
	resolve,
	alt,
	className,
}: {
	id: string;
	resolve: AttachmentPreviewResolver;
	alt: string;
	className?: string;
}): ReactNode {
	const [src, setSrc] = useState<string>();
	const [retry, setRetry] = useState(0);
	useEffect(() => {
		let active = true;
		setSrc(undefined);
		void resolve(id)
			.then((url) => {
				if (active) setSrc(url);
			})
			.catch(() => {});
		return () => {
			active = false;
		};
	}, [id, resolve, retry]);
	return src ? (
		<img
			src={src}
			alt={alt}
			className={className}
			onError={() => {
				if (retry < 2) setRetry((n) => n + 1);
			}}
		/>
	) : (
		<span role="img" aria-label={alt} className={className} />
	);
}
