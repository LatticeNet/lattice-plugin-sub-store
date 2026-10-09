import { computed, ref } from "vue";

import { FILE_TYPE_CONFIG, FILE_TYPE_PLAIN, KIND_COLLECTION, KIND_FILE, KIND_SUB, SOURCE_LOCAL, SOURCE_REMOTE } from "./client";
import type { EditorLanguage } from "./codemirror";
import type { HostContext } from "./host";
import { useEditorExit } from "./useEditorExit";
import { draftFromRecord, emptyDraft, validateDraft, type SubscriptionDraft, type UseSubscriptions } from "./useSubscriptions";

export type FileEditorTab = "display" | "content" | "operations";

export interface FileEditorOptions {
  host: HostContext;
  subs: UseSubscriptions;
  /** Clear anything the list has open, so it does not reappear on the way back. */
  clearListState: () => void;
  /** An overlay or a menu the Escape key belongs to first. */
  overlayOpen: () => boolean;
}

/**
 * The file editor's state: the draft, the two text fields that live beside it
 * (tags and the script's query-parameter allowlist), the section on screen
 * and the unsaved-edit guard.
 *
 * A composable for the same reason the record editor is one (useRecordEditor):
 * `editing` is what the Records screen routes on, so the screen owns it and
 * hands this object to FileEditor.vue, which owns everything it draws. The
 * file editor used to live inside the Files screen, which went when Records
 * took the three per-kind tables into one.
 */
export function useFileEditor(options: FileEditorOptions) {
  const { host, subs } = options;

  const editing = ref(false);
  const editingId = ref<string | null>(null);
  const draft = ref<SubscriptionDraft>(emptyDraft());
  const tagText = ref("");
  const queryParamText = ref("");
  /** Pure presentation: how the content is coloured, never what is saved. */
  const contentLanguageOverride = ref<EditorLanguage | "">("");
  const editorTab = ref<FileEditorTab>("display");

  const draftError = computed(() => (editing.value ? validateDraft(draft.value) : ""));
  const canSave = computed(() => !draftError.value && !subs.saving.value);

  /** Anything that resolves to nodes. A file sourcing a file would recurse. */
  const nodeSources = computed(() =>
    subs.items.value.filter((i) => (i.kind || KIND_SUB) === KIND_SUB || i.kind === KIND_COLLECTION),
  );

  /**
   * The unsaved-edit guard, the same one the record editor uses. The snapshot
   * is the serialised draft plus the two text fields that live outside it,
   * because those are edits too. The language selector is not.
   */
  const exit = useEditorExit({
    editing,
    fingerprint: () => JSON.stringify([draft.value, tagText.value, queryParamText.value]),
    overlayOpen: options.overlayOpen,
    leave: () => cancelEdit(),
  });
  const { markPristine } = exit;

  function startCreate(fileType: string = FILE_TYPE_CONFIG): void {
    options.clearListState();
    subs.clearMessages();
    draft.value = emptyDraft();
    draft.value.kind = KIND_FILE;
    draft.value.fileType = fileType;
    draft.value.source = SOURCE_LOCAL;
    // Most deployments have exactly one thing worth pointing at, and picking it
    // by default is the difference between a form that works and one that saves
    // a config serving no nodes. Plain text has no proxy list to fill, so the
    // default would be a stored setting with no effect.
    draft.value.nodeSource =
      fileType !== FILE_TYPE_PLAIN && nodeSources.value.length === 1 ? nodeSources.value[0]!.id : "";
    tagText.value = "";
    // The allowlist is the guard on what a public share URL may reach in a
    // script. Leaving the previous record's value in place meant a new file
    // silently inherited an allowlist nobody chose for it.
    queryParamText.value = "";
    contentLanguageOverride.value = "";
    editorTab.value = "display";
    editingId.value = null;
    editing.value = true;
    markPristine();
  }

  async function startEdit(id: string): Promise<void> {
    options.clearListState();
    subs.clearMessages();
    const record = await subs.get(id);
    if (!record) return;
    draft.value = draftFromRecord(record);
    if (!draft.value.source) draft.value.source = draft.value.url ? SOURCE_REMOTE : SOURCE_LOCAL;
    tagText.value = draft.value.tags.join(", ");
    queryParamText.value = draft.value.queryParams.join(", ");
    contentLanguageOverride.value = "";
    editorTab.value = "display";
    editingId.value = id;
    editing.value = true;
    markPristine();
    await host.resize();
  }

  function cancelEdit(): void {
    exit.reset();
    editing.value = false;
    editingId.value = null;
    draft.value = emptyDraft();
    subs.preview.value = null;
    // Errors belong to the screen that raised them, and the notice does not: a
    // successful save reports "Saved ..." and then leaves through here.
    subs.clearErrors();
  }

  function splitList(text: string): string[] {
    return text
      .split(/[,\n]/)
      .map((entry) => entry.trim())
      .filter(Boolean);
  }

  async function submit(): Promise<void> {
    draft.value.tags = splitList(tagText.value);
    draft.value.queryParams = splitList(queryParamText.value);
    const ok = await subs.save(draft.value);
    if (ok) cancelEdit();
  }

  return {
    editing,
    editingId,
    draft,
    tagText,
    queryParamText,
    contentLanguageOverride,
    editorTab,
    draftError,
    canSave,
    nodeSources,
    exit,
    startCreate,
    startEdit,
    cancelEdit,
    submit,
  };
}

export type FileEditorState = ReturnType<typeof useFileEditor>;
