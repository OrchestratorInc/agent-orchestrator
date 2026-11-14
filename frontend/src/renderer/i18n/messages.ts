import en from "./en.json";

export const enMessages = en;

export type MessageKey = {
	[K in keyof typeof enMessages]: typeof enMessages[K] extends string ? K : never;
}[keyof typeof enMessages];

type PluralCategory = "zero" | "one" | "two" | "few" | "many" | "other";
export type PluralMessageKey = MessageKey extends infer Key extends string
	? Key extends `${infer Base}_${PluralCategory}`
		? Base
		: never
	: never;

export type MessageCatalog = Record<MessageKey, string>;
