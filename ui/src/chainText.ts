import { t } from "./i18n";
import type { ChainChange } from "./planFlow";

/**
 * One chain change in the reader's locale (planFlow.ts parseChainChange
 * reads core's English line into its parts). A step type is the operator's
 * own name and stays as it is; the two placeholders core writes for a step
 * it could not name are said in words. A line core wrote in a shape this
 * build does not know is shown as core wrote it.
 */
export function chainChangeText(change: ChainChange): string {
  const words = t.plans.chain;
  switch (change.kind) {
    case "many":
      return words.many(change.before, change.after);
    case "more":
      return words.more(change.count);
    case "text":
      return change.text;
    case "step": {
      const type = change.type === "step with no type" ? words.noType : change.type === "unreadable step" ? words.unreadable : change.type;
      const head = words[change.verb](change.position, type);
      const notes = change.notes.map((note) => {
        switch (note.kind) {
          case "disabled":
            return words.notes.disabled;
          case "now-enabled":
            return words.notes.nowEnabled;
          case "now-disabled":
            return words.notes.nowDisabled;
          case "moved":
            return words.notes.moved(note.from);
        }
      });
      return notes.length ? `${head} (${notes.join(", ")})` : head;
    }
  }
}
