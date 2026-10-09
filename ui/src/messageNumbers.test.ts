import { readdirSync, readFileSync, statSync } from "node:fs";
import ts from "typescript";
import { describe, expect, it } from "vitest";

import { en } from "./messages/en";
import { ru } from "./messages/ru";
import { zhCN } from "./messages/zh-CN";

/**
 * Numbers reach the page through Intl with the active locale (i18n.ts): a
 * count as 12,345 in English and 12 345 in Russian, a size through
 * formatBytes. These tests hold the tables and the components to that, so a
 * message that writes `${n}` or a component that divides by 1024 itself
 * fails here rather than on a Russian operator's screen.
 */

const SRC = new URL("./", import.meta.url);
const TABLES = { en: "messages/en.ts", "zh-CN": "messages/zh-CN.ts", ru: "messages/ru.ts" } as const;

/**
 * Numbers that stay digits: the count the operator types back to confirm a
 * delete (the input compares digits), and an HTTP status code.
 */
const DIGITS_ONLY = new Set(["confirm.typeCount", "subs.publishStatus"]);

function source(file: string): ts.SourceFile {
  return ts.createSourceFile(file, readFileSync(new URL(file, SRC), "utf8"), ts.ScriptTarget.Latest, true);
}

function table(file: ts.SourceFile): ts.ObjectLiteralExpression {
  let found: ts.ObjectLiteralExpression | undefined;
  file.forEachChild(function walk(node) {
    if (ts.isVariableDeclaration(node) && node.initializer) {
      const init = ts.isAsExpression(node.initializer) ? node.initializer.expression : node.initializer;
      if (ts.isObjectLiteralExpression(init) && ts.isIdentifier(node.name) && ["en", "zhCN", "ru"].includes(node.name.text)) found = init;
    }
    node.forEachChild(walk);
  });
  if (!found) throw new Error(`${file.fileName} declares no message table`);
  return found;
}

/** Every function message of a table, by its dotted key. */
function messages(object: ts.ObjectLiteralExpression, prefix = ""): [string, ts.ArrowFunction][] {
  const out: [string, ts.ArrowFunction][] = [];
  for (const property of object.properties) {
    if (!ts.isPropertyAssignment(property)) continue;
    const name = ts.isIdentifier(property.name) || ts.isStringLiteral(property.name) ? property.name.text : property.name.getText();
    const key = prefix + name;
    if (ts.isArrowFunction(property.initializer)) out.push([key, property.initializer]);
    else if (ts.isObjectLiteralExpression(property.initializer)) out.push(...messages(property.initializer, `${key}.`));
  }
  return out;
}

const isNumber = (type: ts.TypeNode | undefined): boolean =>
  !!type &&
  (type.kind === ts.SyntaxKind.NumberKeyword ||
    (ts.isUnionTypeNode(type) && type.types.some((member) => member.kind === ts.SyntaxKind.NumberKeyword)));

/** The positions of the number parameters of each English message. */
const numberParameters = new Map(
  messages(table(source(TABLES.en)))
    .map(([key, fn]) => [key, fn.parameters.flatMap((parameter, at) => (isNumber(parameter.type) ? [at] : []))] as const)
    .filter(([key, positions]) => positions.length > 0 && !DIGITS_ONLY.has(key)),
);

/**
 * The places a message interpolates one of its number parameters without
 * formatting it: `${n}`, or `${a || b}`, anything but a call (num, plural,
 * count) or a nested template, which is searched on its own.
 */
function bareNumbers(file: ts.SourceFile): string[] {
  const found: string[] = [];
  for (const [key, fn] of messages(table(file))) {
    const positions = numberParameters.get(key);
    if (!positions) continue;
    const names = new Set(positions.map((at) => fn.parameters[at]?.name.getText(file)));
    fn.body.forEachChild(function walk(node: ts.Node): void {
      if (ts.isTemplateSpan(node)) {
        const bare = (expression: ts.Node): boolean => {
          if (ts.isIdentifier(expression)) return names.has(expression.text);
          if (ts.isCallExpression(expression) || ts.isTemplateLiteral(expression) || ts.isStringLiteral(expression)) return false;
          if (ts.isConditionalExpression(expression)) return bare(expression.whenTrue) || bare(expression.whenFalse);
          if (ts.isParenthesizedExpression(expression)) return bare(expression.expression);
          if (ts.isBinaryExpression(expression)) return bare(expression.left) || bare(expression.right);
          return false;
        };
        if (bare(node.expression)) found.push(`${key}: \${${node.expression.getText(file)}}`);
      }
      node.forEachChild(walk);
    });
  }
  return found;
}

describe("numbers in the message tables", () => {
  it("finds the English messages that take a number", () => {
    // A guard on the guard: if the signatures stop parsing, every table passes.
    expect(numberParameters.size).toBeGreaterThan(100);
    expect(numberParameters.get("map.noteDense")).toEqual([0]);
  });

  for (const [locale, file] of Object.entries(TABLES)) {
    it(`writes every number the ${locale} table states through Intl`, () => {
      expect(bareNumbers(source(file))).toEqual([]);
    });
  }

  it("groups a large count the locale's way", () => {
    const grouped = (locale: string) => new Intl.NumberFormat(locale).format(12345);
    expect(en.map.noteDense(12345)).toContain(grouped("en"));
    expect(zhCN.map.noteDense(12345)).toContain(grouped("zh-CN"));
    expect(ru.map.noteDense(12345)).toContain(grouped("ru"));
    expect(ru.previewSummary.keptOf(4096, 12345)).toContain(grouped("ru"));
    expect(en.documentView.truncated(2000, 12345)).toContain(grouped("en"));
  });

  it("states an inline size over the limit in exact bytes, with Russian's three forms", () => {
    const ruCount = (value: number) => new Intl.NumberFormat("ru").format(value);
    expect(en.draft.tooLarge(262145, 262144)).toBe("Inline content is 262,145 bytes; the limit is 262,144 bytes.");
    expect(ru.draft.tooLarge(262145, 262144)).toContain(`${ruCount(262145)} байт;`);
    expect(ru.draft.tooLarge(262142, 262144)).toContain(`${ruCount(262142)} байта;`);
  });
});

/** The source of every module and component the page ships, comments removed. */
function shippedSources(dir: URL = SRC): [string, string][] {
  const out: [string, string][] = [];
  for (const name of readdirSync(dir)) {
    const child = new URL(name, dir);
    if (statSync(child).isDirectory()) {
      if (name !== "messages") out.push(...shippedSources(new URL(`${name}/`, dir)));
      continue;
    }
    if (!/\.(ts|vue)$/.test(name) || name.endsWith(".test.ts") || name === "i18n.ts") continue;
    const text = readFileSync(child, "utf8")
      .replace(/\/\*[\s\S]*?\*\//g, "")
      .replace(/<!--[\s\S]*?-->/g, "")
      .replace(/(^|[^:])\/\/.*$/gm, "$1");
    out.push([child.pathname.slice(SRC.pathname.length), text]);
  }
  return out;
}

describe("sizes outside i18n.ts", () => {
  it("are never formatted by hand", () => {
    const byHand = shippedSources().flatMap(([file, text]) =>
      [/\/\s*1024\b/, /\btoFixed\(\d\)\s*\}?\s*(B|KB|MB|GB)\b/, /["'`\s](KB|MB|GB|KiB|MiB)["'`]/]
        .filter((pattern) => pattern.test(text))
        .map((pattern) => `${file} matches ${pattern}`),
    );
    expect(byHand).toEqual([]);
  });
});
