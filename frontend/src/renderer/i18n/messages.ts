import en from "./en.json";

// Only English is bundled into the startup graph; it is the fallback and the
// source of the key types. Other locales load on demand (instance.ts
// loadLocaleCatalog). Tests and tools that need every catalog import
// all-messages.ts, which must stay out of runtime code.

/** English is the source-of-truth catalog; keys are typed from it. */
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
