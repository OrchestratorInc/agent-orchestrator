import { isValidElement, type ReactNode } from "react";

export function textOf(children: ReactNode): string {
	if (typeof children === "string") return children;
	if (typeof children === "number") return String(children);
	if (Array.isArray(children)) return children.map(textOf).join("");
	if (isValidElement(children) && children.type === "br") return "\n";
	if (children && typeof children === "object" && "props" in children) {
		return textOf((children as { props?: { children?: ReactNode } }).props?.children);
	}
	return "";
}

const LANGUAGE_CLASS = /language-([\w+#-]+)/;

export function fenceOf(children: ReactNode): { code: string; language?: string } | undefined {
	if (!isValidElement<{ className?: string; children?: ReactNode }>(children)) return undefined;
	return {
		code: textOf(children.props.children).replace(/\n$/, ""),
		language: LANGUAGE_CLASS.exec(children.props.className ?? "")?.[1],
	};
}
