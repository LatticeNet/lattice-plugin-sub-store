/**
 * regexRewrite.ts, the patterns the native engine cannot run, and the one
 * rewrite it can offer for them.
 *
 * The native chain compiles every operator pattern with Go's RE2, which has no
 * lookaround and no backreferences. A stored record that uses them keeps
 * rendering on the fallback bundle and is flagged `regex_incompatible`; a save
 * that would introduce one is refused with that code (s1-plan section 3.1,
 * design 28 "Node filtering"). The refusal names the code, and the editor
 * needs to say which step and what to do instead, so the same reading runs
 * here over the draft's chain.
 *
 * The rewrite is the one idiom the engine recognises (RewriteNegativeLookahead
 * in s1-plan section 2.3): a keep-mode Regex Filter whose single pattern is
 * `^(?!.*(A|B)).*$`, "keep everything that does not mention A or B", is the
 * drop-mode filter `A|B`. Anything else is reported without an offer.
 */

/** One stored pattern RE2 cannot compile, with the rewrite when there is one. */
export interface RegexDiagnostic {
  /** 1-based position in the whole chain, as the chain list numbers it. */
  step: number;
  type: string;
  pattern: string;
  /** The drop-mode pattern for the keep-mode negative-lookahead idiom; "" otherwise. */
  rewrite: string;
}

/** Lookahead, lookbehind, atomic groups and backreferences: what RE2 refuses. */
const RE2_REFUSES = /\(\?(?:[=!>]|<[=!])|\\[1-9]/;

/** Whether the native engine would refuse this pattern. */
export function regexIncompatible(pattern: string): boolean {
  return RE2_REFUSES.test(stripCharacterClasses(pattern));
}

/**
 * Inside `[...]` the constructs above are literal characters, so a class like
 * `[(?!]` must not read as a lookahead. Escapes are kept as they are.
 */
function stripCharacterClasses(pattern: string): string {
  let out = "";
  let inClass = false;
  for (let i = 0; i < pattern.length; i += 1) {
    const ch = pattern[i]!;
    if (ch === "\\") {
      if (!inClass) out += pattern.slice(i, i + 2);
      i += 1;
      continue;
    }
    if (inClass) {
      if (ch === "]") inClass = false;
      continue;
    }
    if (ch === "[") {
      inClass = true;
      // A leading `]` or `^]` is a literal member, not the class's end.
      if (pattern[i + 1] === "^") i += 1;
      if (pattern[i + 1] === "]") i += 1;
      continue;
    }
    out += ch;
  }
  return out;
}

const NEGATIVE_LOOKAHEAD = /^\^\(\?!\.\*(.+)\)\.\*\$$/;

/**
 * `^(?!.*(A|B)).*$` and `^(?!.*A).*$` to `A|B` and `A`; "" for any other
 * shape, and for an inner pattern that would itself need lookaround.
 */
export function rewriteNegativeLookahead(pattern: string): string {
  const match = NEGATIVE_LOOKAHEAD.exec(pattern.trim());
  if (!match) return "";
  let inner = match[1]!;
  // The inner pattern itself must run natively, so a lookaround inside the
  // lookahead is refused before any group around it is unwrapped.
  if (regexIncompatible(inner)) return "";
  // Unwrap one capturing or non-capturing group around the whole alternation;
  // any other `(?` group is not a wrapper.
  const open = inner.startsWith("(?:") ? "(?:" : inner.startsWith("(") && inner[1] !== "?" ? "(" : "";
  if (open && inner.endsWith(")") && balanced(inner.slice(open.length, -1))) inner = inner.slice(open.length, -1);
  if (!inner || !balanced(inner)) return "";
  return inner;
}

/** Every unescaped parenthesis outside a class closes in order. */
function balanced(text: string): boolean {
  let depth = 0;
  const plain = stripCharacterClasses(text).replace(/\\./g, "");
  for (const ch of plain) {
    if (ch === "(") depth += 1;
    if (ch === ")") depth -= 1;
    if (depth < 0) return false;
  }
  return depth === 0;
}

interface StepLike {
  type?: unknown;
  disabled?: unknown;
  args?: unknown;
}

function strings(value: unknown): string[] {
  if (typeof value === "string") return [value];
  if (!Array.isArray(value)) return [];
  return value.filter((entry): entry is string => typeof entry === "string");
}

/**
 * The patterns a step compiles, by operator: Regex Filter's `regex` (or a
 * migrated record's `value`), Regex Delete and Regex Sort's `value`, and the
 * `expr` of each Regex Rename pair.
 */
export function stepPatterns(step: unknown): string[] {
  const { type, args } = (step ?? {}) as StepLike;
  const bag = (args ?? {}) as Record<string, unknown>;
  if (type === "Regex Filter") return strings(bag.regex ?? bag.value);
  if (type === "Regex Delete Operator" || type === "Regex Sort Operator") return strings(bag.value);
  if (type === "Regex Rename Operator" && Array.isArray(bag.value)) {
    return bag.value.flatMap((pair) => strings((pair as { expr?: unknown } | null)?.expr));
  }
  return [];
}

/** Every pattern in the enabled steps of a chain that the native engine refuses. */
export function regexDiagnostics(chain: readonly unknown[]): RegexDiagnostic[] {
  const out: RegexDiagnostic[] = [];
  chain.forEach((step, index) => {
    const { type, disabled, args } = (step ?? {}) as StepLike;
    if (disabled === true || typeof type !== "string") return;
    const patterns = stepPatterns(step);
    const keep = (args as { keep?: unknown } | undefined)?.keep !== false;
    for (const pattern of patterns) {
      if (!regexIncompatible(pattern)) continue;
      // Only a single-pattern keep filter is the idiom: with other patterns
      // beside it, turning the step into a drop filter would change what the
      // rest of them keep.
      const rewrite = type === "Regex Filter" && keep && patterns.length === 1 ? rewriteNegativeLookahead(pattern) : "";
      out.push({ step: index + 1, type, pattern, rewrite });
    }
  });
  return out;
}

/**
 * The chain with one diagnostic's rewrite applied: the step becomes a
 * drop-mode Regex Filter over the rewritten pattern, every other field of the
 * step (its custom name, its id) untouched. The chain as given when the
 * diagnostic has no rewrite or no longer matches the step.
 */
export function applyRewrite(chain: readonly unknown[], diagnostic: RegexDiagnostic): unknown[] {
  const index = diagnostic.step - 1;
  const step = chain[index] as StepLike | undefined;
  if (!diagnostic.rewrite || !step || step.type !== "Regex Filter") return [...chain];
  const patterns = stepPatterns(step);
  if (patterns.length !== 1 || patterns[0] !== diagnostic.pattern) return [...chain];
  const args = { ...((step.args ?? {}) as Record<string, unknown>) };
  delete args.value;
  const next = { ...step, args: { ...args, regex: [diagnostic.rewrite], keep: false } };
  return chain.map((entry, at) => (at === index ? next : entry));
}
