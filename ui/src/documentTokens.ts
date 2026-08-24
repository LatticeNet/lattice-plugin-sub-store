import type { EditorLanguage } from "./codemirror";

/**
 * Line tokenizers for the read-only document view.
 *
 * The preview used to mount CodeMirror, which cannot style itself inside the
 * plugin frame: the policy has no 'unsafe-inline' and CodeMirror installs its
 * layout and highlighting as stylesheets it creates at runtime, so all of it
 * was dropped and the document rendered as unstyled text. Rendering the
 * document ourselves puts every rule in the bundle's own stylesheet, which the
 * policy already allows, and drops a 422 KB chunk from the read path.
 *
 * These are highlighters, not parsers. They colour one line at a time with no
 * memory of the last, which is exactly wrong for a multi-line string and
 * exactly right for a viewer that must never be slower or more fragile than
 * the text it is showing. Where a language cannot be read a line at a time,
 * the honest output is `plain`.
 */

/** The colour roles the stylesheet knows. Nothing else may be emitted. */
export type TokenKind =
  | "plain"
  | "comment"
  | "key"
  | "string"
  | "number"
  | "literal"
  | "punct"
  | "scheme"
  | "host"
  | "param"
  | "name";

export interface Token {
  kind: TokenKind;
  text: string;
}

function plain(text: string): Token[] {
  return text ? [{ kind: "plain", text }] : [];
}

/** Push text as one token, skipping the empty string so lines stay tidy. */
function push(out: Token[], kind: TokenKind, text: string): void {
  if (text) out.push({ kind, text });
}

/**
 * A proxy URI line, which is what most subscription documents are made of.
 * The parts an operator actually scans for are the node name in the fragment
 * and the endpoint, so those get the colour; the credential in the userinfo
 * deliberately does not, because drawing the eye to it is the opposite of what
 * this surface is for.
 */
const URI_LINE = /^([a-z][a-z0-9+.-]*):\/\/([^?#\s]*)(\?[^#\s]*)?(#\S*)?$/i;

function tokenizeURI(line: string): Token[] | null {
  const match = URI_LINE.exec(line);
  if (!match) return null;
  const [, scheme, authority, query, fragment] = match;
  const out: Token[] = [];
  push(out, "scheme", scheme);
  push(out, "punct", "://");
  const at = authority.lastIndexOf("@");
  if (at >= 0) {
    push(out, "plain", authority.slice(0, at + 1));
    push(out, "host", authority.slice(at + 1));
  } else {
    push(out, "host", authority);
  }
  if (query) {
    push(out, "punct", "?");
    const parts = query.slice(1).split("&");
    parts.forEach((part, index) => {
      if (index > 0) push(out, "punct", "&");
      const eq = part.indexOf("=");
      if (eq < 0) {
        push(out, "param", part);
        return;
      }
      push(out, "param", part.slice(0, eq));
      push(out, "punct", "=");
      push(out, "plain", part.slice(eq + 1));
    });
  }
  if (fragment) {
    push(out, "punct", "#");
    push(out, "name", fragment.slice(1));
  }
  return out;
}

const YAML_KEY = /^(\s*(?:-\s+)?)([A-Za-z0-9_.-]+)(\s*:)(\s*)(.*)$/;
const SCALAR = /^(-?\d+(?:\.\d+)?|true|false|null|~)$/i;

function tokenizeScalar(text: string): Token[] {
  const trimmed = text.trim();
  if (!trimmed) return plain(text);
  const lead = text.slice(0, text.indexOf(trimmed));
  const tail = text.slice(lead.length + trimmed.length);
  const out: Token[] = [];
  push(out, "plain", lead);
  if (/^["'].*["']$/.test(trimmed)) push(out, "string", trimmed);
  else if (SCALAR.test(trimmed)) push(out, "number", trimmed);
  else push(out, "plain", trimmed);
  push(out, "plain", tail);
  return out;
}

function tokenizeYAML(line: string): Token[] {
  const hash = line.indexOf("#");
  if (hash >= 0 && line.slice(0, hash).trim() === "") {
    return [{ kind: "comment", text: line }];
  }
  const match = YAML_KEY.exec(line);
  if (!match) return plain(line);
  const [, lead, key, colon, gap, value] = match;
  const out: Token[] = [];
  push(out, "plain", lead);
  push(out, "key", key);
  push(out, "punct", colon);
  push(out, "plain", gap);
  return out.concat(tokenizeScalar(value));
}

const JSON_PIECE = /("(?:[^"\\]|\\.)*"\s*:?)|(-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)|(\btrue\b|\bfalse\b|\bnull\b)|([[\]{},:])/g;

function tokenizeJSON(line: string): Token[] {
  const out: Token[] = [];
  let last = 0;
  for (const match of line.matchAll(JSON_PIECE)) {
    const at = match.index ?? 0;
    push(out, "plain", line.slice(last, at));
    const [text, quoted, num, literal, punct] = match;
    if (quoted !== undefined) {
      // A quoted run that a colon follows is a property, not a value.
      if (quoted.trimEnd().endsWith(":")) {
        const colon = quoted.lastIndexOf(":");
        push(out, "key", quoted.slice(0, colon));
        push(out, "punct", quoted.slice(colon));
      } else {
        push(out, "string", quoted);
      }
    } else if (num !== undefined) push(out, "number", num);
    else if (literal !== undefined) push(out, "literal", literal);
    else if (punct !== undefined) push(out, "punct", punct);
    last = at + text.length;
  }
  push(out, "plain", line.slice(last));
  return out;
}

const JS_KEYWORDS =
  /\b(const|let|var|function|return|if|else|for|of|in|while|await|async|new|try|catch|finally|throw|typeof|class|export|import|from|default|delete)\b/g;
const JS_PIECE =
  /(\/\/.*$)|('(?:[^'\\]|\\.)*'|"(?:[^"\\]|\\.)*"|`(?:[^`\\]|\\.)*`)|(-?\b\d+(?:\.\d+)?\b)/g;

function tokenizeJavaScript(line: string): Token[] {
  const out: Token[] = [];
  let last = 0;
  for (const match of line.matchAll(JS_PIECE)) {
    const at = match.index ?? 0;
    out.push(...tokenizeJSWords(line.slice(last, at)));
    const [text, comment, str, num] = match;
    if (comment !== undefined) push(out, "comment", comment);
    else if (str !== undefined) push(out, "string", str);
    else if (num !== undefined) push(out, "number", num);
    last = at + text.length;
  }
  out.push(...tokenizeJSWords(line.slice(last)));
  return out;
}

function tokenizeJSWords(text: string): Token[] {
  if (!text) return [];
  const out: Token[] = [];
  let last = 0;
  for (const match of text.matchAll(JS_KEYWORDS)) {
    const at = match.index ?? 0;
    push(out, "plain", text.slice(last, at));
    push(out, "literal", match[0]);
    last = at + match[0].length;
  }
  push(out, "plain", text.slice(last));
  return out;
}

const INI_SECTION = /^(\s*)(\[[^\]]*\])(\s*)$/;
const INI_ENTRY = /^(\s*)([^=\s][^=]*?)(\s*=\s*)(.*)$/;

function tokenizeINI(line: string): Token[] {
  if (/^\s*[#;]/.test(line)) return [{ kind: "comment", text: line }];
  const section = INI_SECTION.exec(line);
  if (section) {
    const [, lead, head, tail] = section;
    const out: Token[] = [];
    push(out, "plain", lead);
    push(out, "key", head);
    push(out, "plain", tail);
    return out;
  }
  const entry = INI_ENTRY.exec(line);
  if (!entry) return plain(line);
  const [, lead, key, sep, value] = entry;
  const out: Token[] = [];
  push(out, "plain", lead);
  push(out, "key", key);
  push(out, "punct", sep);
  push(out, "plain", value);
  return out;
}

function tokenizePlain(line: string): Token[] {
  if (/^\s*#/.test(line) && !URI_LINE.test(line.trim())) {
    return [{ kind: "comment", text: line }];
  }
  return tokenizeURI(line.trim() === line ? line : line.trim()) ?? plain(line);
}

/** One line in, coloured spans out. Never throws: a viewer must always render. */
export function tokenizeLine(line: string, language: EditorLanguage): Token[] {
  try {
    switch (language) {
      case "yaml":
        return tokenizeURI(line) ?? tokenizeYAML(line);
      case "json":
        return tokenizeJSON(line);
      case "javascript":
        return tokenizeJavaScript(line);
      case "ini":
        return tokenizeURI(line) ?? tokenizeINI(line);
      default:
        return tokenizePlain(line);
    }
  } catch {
    return plain(line);
  }
}

/**
 * How many lines the viewer will render at once. A rendered client document is
 * usually tens of lines; a Mihomo configuration can be thousands, and building
 * a span tree for all of them costs more than anyone reading the top of it
 * wants to pay. Past the cap the viewer says what it is holding back rather
 * than quietly showing a prefix.
 */
export const MAX_RENDERED_LINES = 2000;

export interface DocumentLines {
  lines: Token[][];
  total: number;
  hidden: number;
}

export function tokenizeDocument(text: string, language: EditorLanguage): DocumentLines {
  const all = text.split("\n");
  // A trailing newline is punctuation, not an empty last line to number.
  if (all.length > 1 && all[all.length - 1] === "") all.pop();
  const shown = all.slice(0, MAX_RENDERED_LINES);
  return {
    lines: shown.map((line) => tokenizeLine(line, language)),
    total: all.length,
    hidden: all.length - shown.length,
  };
}
