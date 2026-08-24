<script setup lang="ts">
/**
 * DocumentView, the read-only render of a produced document.
 *
 * This is not an editor and deliberately does not use one. CodeMirror installs
 * its layout and highlighting as stylesheets it creates at runtime, and the
 * plugin frame's policy has no 'unsafe-inline', so every one of those rules is
 * dropped: in production the preview showed line numbers and nothing else.
 * Everything here is styled by the bundle's own stylesheet, which the policy
 * already allows.
 *
 * Two properties this surface has to keep. The numbers come from a CSS counter,
 * so selecting the document and copying it yields the document and not a column
 * of digits. And the viewer owns its scrolling: the previous preview removed the
 * editor's height ceiling, which left nothing between the document and the
 * sheet, so a long render simply ran off the bottom with no way to reach it.
 */
import { computed } from "vue";

import type { EditorLanguage } from "../codemirror";
import { tokenizeDocument } from "../documentTokens";

const props = withDefaults(
  defineProps<{
    text: string;
    language?: EditorLanguage;
    /** Visible height, in lines, before the viewer scrolls. */
    rows?: number;
    ariaLabelledby?: string;
  }>(),
  { language: "plain", rows: 24, ariaLabelledby: undefined },
);

const parsed = computed(() => tokenizeDocument(props.text, props.language));
</script>

<template>
  <div class="doc-view" :style="{ '--doc-rows': String(rows) }">
    <!-- Focusable so a keyboard reader can scroll it, and labelled so what it
         holds is announced rather than being an unnamed scrollable region. -->
    <div
      class="doc-scroll"
      data-document-view="true"
      tabindex="0"
      role="group"
      :aria-labelledby="ariaLabelledby"
    >
      <ol class="doc-lines">
        <li v-for="(line, index) in parsed.lines" :key="index" class="doc-line">
          <!-- One grid item holds the whole line. Letting each token be its own
               item put every span on its own row. -->
          <code class="doc-code"
            ><span v-for="(token, at) in line" :key="at" :class="`tok tok-${token.kind}`">{{ token.text }}</span
            ><span v-if="line.length === 0" class="doc-empty"></span
          ></code>
        </li>
      </ol>
    </div>
    <p v-if="parsed.hidden > 0" class="doc-truncated">
      Showing the first {{ parsed.lines.length }} of {{ parsed.total }} lines. Copy the document to get all of it.
    </p>
  </div>
</template>
