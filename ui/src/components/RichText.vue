<script setup lang="ts">
import type { Rich } from "../messages/format";

/**
 * A message with inline code in it: the parts in order, identifiers in
 * <code>. The message table owns the order, so a locale can put an
 * identifier wherever its sentence needs it.
 */
defineProps<{ parts: Rich }>();
</script>

<template>
  <template v-for="(part, index) in parts" :key="index"><code v-if="typeof part !== 'string'">{{ part.code }}</code><template v-else>{{ part }}</template></template>
</template>
