/**
 * messages/format.ts, the locale-bound pieces a message table needs: the
 * plural form for a count and the count written the locale's way.
 *
 * Kept apart from i18n.ts so a table can import it without importing the
 * module that imports every table.
 */

const pluralRules = new Map<string, Intl.PluralRules>();
const numberFormats = new Map<string, Intl.NumberFormat>();

export function numberFormat(locale: string, options?: Intl.NumberFormatOptions): Intl.NumberFormat {
  const key = options ? `${locale}|${JSON.stringify(options)}` : locale;
  let format = numberFormats.get(key);
  if (!format) {
    format = new Intl.NumberFormat(locale, options);
    numberFormats.set(key, format);
  }
  return format;
}

function rulesFor(locale: string): Intl.PluralRules {
  let rules = pluralRules.get(locale);
  if (!rules) {
    rules = new Intl.PluralRules(locale);
    pluralRules.set(locale, rules);
  }
  return rules;
}

/** English has one and other. */
export interface EnglishForms {
  one: string;
  other: string;
}

/** Chinese has one form for every count. */
export interface ChineseForms {
  other: string;
}

/**
 * Russian has one (1, 21, 101), few (2 to 4, 22 to 24) and many (0, 5 to 20,
 * 25 to 30). Its fourth category, other, covers fractions, which take the
 * genitive singular that few already uses, so it falls back to few.
 */
export interface RussianForms {
  one: string;
  few: string;
  many: string;
}

type AnyForms = Partial<Record<Intl.LDMLPluralRule, string>>;

/**
 * The plural function of one locale: the form its rules pick for the count,
 * with `{n}` replaced by the count written that locale's way (12,345 in
 * English, 12 345 in Russian).
 */
export function pluralIn<Forms extends EnglishForms | ChineseForms | RussianForms>(
  locale: string,
): (count: number, forms: Forms) => string {
  return (count, forms) => {
    const all = forms as AnyForms;
    const category = rulesFor(locale).select(count);
    const form = all[category] ?? all.other ?? all.few ?? "";
    return form.replace("{n}", numberFormat(locale).format(count));
  };
}

/**
 * A sentence with inline code in it, as parts: plain text, and `{ code }`
 * for an identifier drawn in the mono face (RichText.vue). A locale may
 * order and split the parts as its grammar needs.
 */
export type Rich = readonly (string | { code: string })[];
