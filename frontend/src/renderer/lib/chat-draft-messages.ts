import { useTranslation } from "react-i18next";

export function useChatDraftTranslation(): (message: string | null | undefined) => string | undefined {
	const { t } = useTranslation();
	return (message) => message?.startsWith("chat.draft.") ? t(message, { defaultValue: message }) : message ?? undefined;
}
