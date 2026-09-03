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
 * of digits. And it is not a scroller: one scroll per document (DESIGN-PROGRAM
 * 1), so the viewer is as tall as what it holds and the page scrolls it. It
 * used to cap itself at a row count and scroll inside, which put a second
 * wheel inside every panel that showed a document. Growth is bounded by the
 * tokenizer, which stops at MAX_RENDERED_LINES and says what it held back.
 */
import { computed } from "vue";

import type { EditorLanguage } from "../codemirror";
import { tokenizeDocument } from "../documentTokens";

const props = withDefaults(
  defineProps<{
    text: string;
    language?: EditorLanguage;
    ariaLabelledby?: string;
  }>(),
  { language: "plain", ariaLabelledby: undefined },
);

const parsed = computed(() => tokenizeDocument(props.text, props.language));
</script>

<template>
  <div class="doc-view">
    <!-- Labelled so what it holds is announced as a named group. Not a tab
         stop: nothing here scrolls, so a focusable box would be a stop that
         does nothing. -->
    <div
      class="doc-scroll"
      data-document-view="true"
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
