import { afterEach, describe, expect, it } from "vitest";

import {
  compareText,
  currentLocale,
  formatBytes,
  formatList,
  formatPercent,
  formatRelativeTime,
  matchLocale,
  messagesFor,
  setLocale,
  t,
  type Locale,
} from "./i18n";
import { en } from "./messages/en";
import { ru } from "./messages/ru";
import { zhCN } from "./messages/zh-CN";
import { formatAge } from "./observedAge";
import { formatExpiry } from "./pipeline";

const TABLES = [
  ["zh-CN", zhCN],
  ["ru", ru],
] as const;

type Shape = "string" | "function" | "array" | "object";

function shapeOf(value: unknown): Shape {
  if (typeof value === "string") return "string";
  if (typeof value === "function") return "function";
  if (Array.isArray(value)) return "array";
  return "object";
}

/** Every key path of a table with what sits there; arrays are leaves, their length is the locale's. */
function paths(table: unknown, prefix = ""): Map<string, Shape> {
  const out = new Map<string, Shape>();
  for (const [key, value] of Object.entries(table as Record<string, unknown>)) {
    const path = prefix ? `${prefix}.${key}` : key;
    const shape = shapeOf(value);
    out.set(path, shape);
    if (shape === "object") for (const [inner, innerShape] of paths(value, path)) out.set(inner, innerShape);
  }
  return out;
}

/**
 * What a message says, for every leaf: a string as it is, an array's text
 * parts, and a function called with sample values. Arguments are tried as
 * strings, then numbers, then lists, because the table's functions take
 * names, counts and lists, and the scan needs only one call that answers.
 */
function texts(table: unknown, prefix = ""): Array<[string, string]> {
  const out: Array<[string, string]> = [];
  for (const [key, value] of Object.entries(table as Record<string, unknown>)) {
    const path = prefix ? `${prefix}.${key}` : key;
    if (typeof value === "string") out.push([path, value]);
    else if (Array.isArray(value)) {
      for (const part of value) out.push([path, typeof part === "string" ? part : (part as { code: string }).code]);
    } else if (typeof value === "function") {
      const fn = value as (...args: unknown[]) => unknown;
      const samples: unknown[][] = [
        Array.from({ length: fn.length }, () => "x"),
        Array.from({ length: fn.length }, () => 2),
        Array.from({ length: fn.length }, () => [1, 2]),
      ];
      for (const sample of samples) {
        try {
          out.push([path, String(fn(...sample))]);
          break;
        } catch {
          // The next kind of sample, then.
        }
      }
      // A count of one, which takes its own form in English and Russian.
      try {
        out.push([path, String(fn(...Array.from({ length: fn.length }, () => 1)))]);
      } catch {
        // Not every message takes a count.
      }
    } else if (value && typeof value === "object") out.push(...texts(value, path));
  }
  return out;
}

/** Strings that are the same in every language: protocol and product names, units, examples of syntax. */
const UNTRANSLATED = new Set([
  "B",
  "KB",
  "MB",
  "GB",
  "TB",
  "esc",
  "UDP",
  "TCP Fast Open",
  "VMess AEAD",
  "Cloudflare",
  "Google",
  "Ali",
  "Tencent",
  "home, backup",
  "phone, laptop",
  ", ",
]);

describe("the message tables", () => {
  const english = paths(en);

  it.each(TABLES)("%s has every English key with the same kind of value, and nothing else", (_locale, table) => {
    const translated = paths(table);
    expect([...translated.keys()].sort()).toEqual([...english.keys()].sort());
    for (const [path, shape] of english) expect(translated.get(path), path).toBe(shape);
  });

  it.each([["en", en] as const, ...TABLES])("%s has no empty message and no em or en dash", (_locale, table) => {
    const all = texts(table);
    expect(all.length).toBeGreaterThan(900);
    for (const [path, text] of all) {
      expect(text.trim(), path).not.toBe("");
      expect(text, path).not.toMatch(/[–—]/);
    }
  });

  it.each(TABLES)("%s translates every string that is not a name, a unit or punctuation", (_locale, table) => {
    // String leaves and the text parts of a rich message, by path; code parts are identifiers.
    const leaves = (value: unknown, path = ""): Array<[string, string]> => {
      if (typeof value === "string") return [[path, value]];
      if (Array.isArray(value)) return value.flatMap((part, index) => (typeof part === "string" ? [[`${path}[${index}]`, part] as [string, string]] : []));
      if (value && typeof value === "object") return Object.entries(value).flatMap(([key, inner]) => leaves(inner, path ? `${path}.${key}` : key));
      return [];
    };
    const translated = new Map(leaves(table));
    const left = leaves(en)
      .filter(([path, text]) => translated.get(path) === text && /[A-Za-z]{2}/.test(text) && !UNTRANSLATED.has(text))
      .map(([path, text]) => `${path}: ${text}`);
    expect(left).toEqual([]);
  });

  it("writes Chinese in Chinese and Russian in Cyrillic", () => {
    expect(zhCN.records.layer).toMatch(/[一-鿿]/);
    expect(zhCN.shell.description).toMatch(/[一-鿿]/);
    expect(ru.records.layer).toMatch(/[Ѐ-ӿ]/);
    expect(ru.shell.description).toMatch(/[Ѐ-ӿ]/);
  });
});

describe("the locale", () => {
  afterEach(async () => {
    await setLocale("en");
    delete (globalThis as { document?: unknown }).document;
  });

  it("follows the host's tag by its language, and reads English for anything else", () => {
    const cases: Array<[string | null | undefined, Locale]> = [
      ["zh-CN", "zh-CN"],
      ["zh", "zh-CN"],
      ["zh-Hans-CN", "zh-CN"],
      ["zh_CN", "zh-CN"],
      ["ZH-tw", "zh-CN"],
      ["ru", "ru"],
      ["ru-RU", "ru"],
      ["en-US", "en"],
      ["en", "en"],
      ["fr-FR", "en"],
      ["", "en"],
      [undefined, "en"],
      [null, "en"],
    ];
    for (const [tag, want] of cases) expect(matchLocale(tag), String(tag)).toBe(want);
  });

  it("switches the table once it has arrived, and says the language on <html lang>", async () => {
    const root = { lang: "en" };
    (globalThis as { document?: unknown }).document = { documentElement: root };
    expect(t.records.layer).toBe("Records");
    const pending = setLocale("ru-RU");
    expect(await pending).toBe("ru");
    expect(currentLocale()).toBe("ru");
    expect(t.records.layer).toBe("Записи");
    expect(root.lang).toBe("ru");
    await setLocale("zh-CN");
    expect(t.records.layer).toBe("记录");
    expect(root.lang).toBe("zh-CN");
    await setLocale("de");
    expect(t.records.layer).toBe("Records");
    expect(root.lang).toBe("en");
  });

  it("lets the last request win when an earlier table is still loading", async () => {
    const first = setLocale("ru");
    const second = setLocale("zh-CN");
    await Promise.all([first, second]);
    expect(currentLocale()).toBe("zh-CN");
    expect(t.layers.shares).toBe("分享");
  });

  it("hands every locale's table to a caller that asks", async () => {
    expect(await messagesFor("en")).toBe(en);
    expect(await messagesFor("zh-CN")).toBe(zhCN);
    expect(await messagesFor("ru")).toBe(ru);
  });
});

describe("plural forms", () => {
  it("are one and other in English", () => {
    expect(en.nouns.records(1)).toBe("1 record");
    expect(en.nouns.records(2)).toBe("2 records");
    expect(en.nouns.records(0)).toBe("0 records");
    expect(en.nouns.records(12345)).toBe("12,345 records");
  });

  it("are one, few and many in Russian", () => {
    const forms = [1, 2, 4, 5, 11, 12, 14, 21, 22, 25, 101, 111].map((n) => ru.nouns.records(n));
    expect(forms).toEqual([
      "1 запись",
      "2 записи",
      "4 записи",
      "5 записей",
      "11 записей",
      "12 записей",
      "14 записей",
      "21 запись",
      "22 записи",
      "25 записей",
      "101 запись",
      "111 записей",
    ]);
    expect(ru.nouns.records(0)).toBe("0 записей");
    // Russian groups thousands with a no-break space.
    expect(ru.nouns.nodes(12345)).toBe("12 345 узлов");
  });

  it("are one form with a measure word in Chinese", () => {
    expect(zhCN.nouns.nodes(1)).toBe("1 个节点");
    expect(zhCN.nouns.nodes(1204)).toBe("1,204 个节点");
    expect(zhCN.nouns.records(3)).toBe("3 条记录");
  });
});

describe("Intl formatting follows the locale", () => {
  const NOW = Date.parse("2026-08-10T12:00:00Z");
  const DAY = 86_400_000;

  afterEach(async () => {
    await setLocale("en");
  });

  it("writes relative times the locale's way", async () => {
    expect(formatRelativeTime("2026-08-10T09:00:00Z", NOW)).toBe("3h ago");
    await setLocale("zh-CN");
    expect(formatRelativeTime("2026-08-10T09:00:00Z", NOW)).toBe("3小时前");
    expect(formatRelativeTime("2026-08-10T11:59:40Z", NOW)).toBe("刚刚");
    expect(formatRelativeTime("2026-06-01T12:00:00Z", NOW)).toBe("2026年6月1日");
    await setLocale("ru");
    expect(formatRelativeTime("2026-08-10T09:00:00Z", NOW)).toBe("3 ч назад");
    expect(formatRelativeTime("2026-08-10T11:45:00Z", NOW)).toBe("15 мин. назад");
    expect(formatRelativeTime("2026-08-10T11:59:40Z", NOW)).toBe("только что");
  });

  it("writes an expiry as a day count in words", async () => {
    const at = (days: number) => ({ expire: Math.floor((NOW + days * DAY + 3_600_000) / 1000) });
    await setLocale("zh-CN");
    expect(formatExpiry(at(6), NOW)).toBe("6天后到期");
    expect(formatExpiry(at(1), NOW)).toBe("明天到期");
    expect(formatExpiry({ expire: Math.floor((NOW - 3 * DAY) / 1000) }, NOW)).toBe("3天前已到期");
    await setLocale("ru");
    expect(formatExpiry(at(6), NOW)).toBe("истекает через 6 дней");
    expect(formatExpiry(at(1), NOW)).toBe("истекает завтра");
    expect(formatExpiry({ expire: Math.floor((NOW - 3 * DAY) / 1000) }, NOW)).toBe("истекла 3 дня назад");
  });

  it("writes sizes, percentages and ages with the locale's numbers and units", async () => {
    expect(formatBytes(1536)).toBe("1.5 KB");
    expect(formatPercent(0.82)).toBe("82%");
    expect(formatAge(13_400)).toBe("13s");
    await setLocale("ru");
    expect(formatBytes(1536)).toBe("1,5 КБ");
    expect(formatBytes(500 * 1024 ** 3)).toBe("500 ГБ");
    expect(formatPercent(0.82)).toBe("82 %");
    expect(formatAge(13_400)).toBe("13 с");
    await setLocale("zh-CN");
    expect(formatBytes(1536)).toBe("1.5 KB");
    expect(formatAge(13_400)).toBe("13秒");
  });

  it("sorts and joins the way the locale does", async () => {
    await setLocale("ru");
    expect(["Яблоко", "арбуз", "Борщ"].sort(compareText)).toEqual(["арбуз", "Борщ", "Яблоко"]);
    expect(formatList(["a", "b", "c"])).toBe("a, b и c");
    await setLocale("zh-CN");
    expect(formatList(["a", "b", "c"])).toBe("a、b和c");
  });
});
