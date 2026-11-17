<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'
import { AlertTriangle, Loader2 } from 'lucide-vue-next'

import type { LiveChunk } from '@/stores/recording'

const props = defineProps<{ chunks: LiveChunk[]; recording: boolean }>()

const container = ref<HTMLElement | null>(null)
const pinned = ref(true)

// Auto-scroll, but stop the moment the user scrolls up to read something —
// yanking the view away mid-sentence is worse than a stale scroll position.
function onScroll() {
  const el = container.value
  if (!el) return
  pinned.value = el.scrollHeight - el.scrollTop - el.clientHeight < 48
}

watch(
  () => props.chunks.length,
  async () => {
    if (!pinned.value) return
    await nextTick()
    const el = container.value
    if (el) el.scrollTop = el.scrollHeight
  },
)
</script>

<template>
  <div
    ref="container"
    class="h-80 overflow-y-auto rounded-md border border-slate-200 bg-white p-4 dark:border-slate-800 dark:bg-slate-900"
    role="log"
    aria-live="polite"
    aria-label="Transkrip langsung"
    @scroll="onScroll"
  >
    <p v-if="chunks.length === 0" class="text-sm text-slate-500 dark:text-slate-400">
      {{
        recording
          ? 'Menunggu potongan audio pertama selesai ditranskrip…'
          : 'Transkrip akan muncul di sini saat rapat berjalan.'
      }}
    </p>

    <div v-else class="space-y-3">
      <div v-for="chunk in chunks" :key="chunk.index" class="text-sm leading-relaxed">
        <template v-if="chunk.status === 'COMPLETED'">
          <p v-if="chunk.text">{{ chunk.text }}</p>
          <p v-else class="italic text-slate-400 dark:text-slate-500">(hening)</p>
        </template>

        <p
          v-else-if="chunk.status === 'PROCESSING'"
          class="inline-flex items-center gap-2 text-slate-400 dark:text-slate-500"
        >
          <Loader2 class="h-3.5 w-3.5 animate-spin" aria-hidden="true" />
          Bagian {{ chunk.index + 1 }} sedang ditranskrip…
        </p>

        <p v-else class="inline-flex items-start gap-2 text-red-600 dark:text-red-400">
          <AlertTriangle class="mt-0.5 h-3.5 w-3.5 shrink-0" aria-hidden="true" />
          <span>Bagian {{ chunk.index + 1 }} gagal: {{ chunk.error ?? 'transkripsi gagal' }}</span>
        </p>
      </div>
    </div>
  </div>
</template>
