<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink, useRoute } from 'vue-router'

import { useRecordingStore } from '@/stores/recording'
import { formatDuration } from '@/utils/format'

/**
 * A way back to a recording in progress.
 *
 * The recording now survives navigation, which means the user can wander off
 * and lose track of it. Without this, a meeting could run for an hour with
 * nothing on screen to say so.
 */
const store = useRecordingStore()
const route = useRoute()

// Redundant while you are looking at the recording itself.
const visible = computed(
  () => store.active && store.meetingId !== null && route.params.id !== store.meetingId,
)

const elapsed = computed(() => formatDuration(store.recorder.elapsedSeconds))
</script>

<template>
  <RouterLink
    v-if="visible"
    :to="`/meetings/${store.meetingId}`"
    class="inline-flex items-center gap-2 rounded-full border px-3 py-1 text-xs font-medium transition"
    :class="
      store.paused
        ? 'border-amber-300 bg-amber-50 text-amber-900 hover:bg-amber-100 dark:border-amber-900 dark:bg-amber-950 dark:text-amber-200'
        : 'border-red-300 bg-red-50 text-red-900 hover:bg-red-100 dark:border-red-900 dark:bg-red-950 dark:text-red-200'
    "
  >
    <span
      class="inline-flex h-2 w-2 rounded-full"
      :class="store.paused ? 'bg-amber-500' : 'animate-pulse bg-red-600'"
      aria-hidden="true"
    />
    <span class="font-mono tabular-nums">{{ elapsed }}</span>
    <span>{{ store.paused ? 'Dijeda' : 'Merekam' }}</span>
  </RouterLink>
</template>
