import { en, type Messages } from "../src/messages/en";
import { ru } from "../src/messages/ru";
import { zhCN } from "../src/messages/zh-CN";

/**
 * The page's own message tables, so a test finds a control by the name the
 * page gives it in the locale it runs in, never by English typed into the
 * test. `m` is the English table, for the drives that run in English.
 */
export type Locale = "en" | "zh-CN" | "ru";

export const TABLES: Record<Locale, Messages> = { en, "zh-CN": zhCN, ru };
export const LOCALES = Object.keys(TABLES) as Locale[];
export const m: Messages = en;

/** A name that starts with `text`, as a tab with its count after it does. */
export const startsWith = (text: string): RegExp => new RegExp(`^${text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}`);
