<script setup lang="ts">
import { computed, ref } from 'vue'
import { AlertTriangle, Check, Copy } from 'lucide-vue-next'

import { formatDuration } from '@/utils/format'
import type { TranscriptChunk } from '@/types/api'

const props = defineProps<{ chunks: TranscriptChunk[] }>()

const copied = ref(false)

const fullText = computed(() =>
  props.chunks
    .filter((c) => c.status === 'COMPLETED' && c.transcript_text.trim())
    .map((c) => c.transcript_text.trim())
    .join('\n\n'),
)

const failedCount = computed(() => props.chunks.filter((c) => c.status === 'FAILED').length)

async function copy() {
  try {
    await navigator.clipboard.writeText(fullText.value)
    copied.value = true
    window.setTimeout(() => (copied.value = false), 2000)
  } catch {
    // Clipboard access can be blocked; the text is on screen to select anyway.
  }
}
</script>

<template>
  <section aria-labelledby="transcript-heading">
    <div class="flex items-center justify-between gap-4">
      <h3 id="transcript-heading" class="text-sm font-semibold">Transkrip</h3>

      <button
        v-if="fullText"
        type="button"
        class="no-print inline-flex items-center gap-1.5 rounded-md border border-slate-300 px-2.5 py-1 text-xs font-medium hover:bg-slate-50 dark:border-slate-700 dark:hover:bg-slate-900"
        @click="copy"
      >
        <component :is="copied ? Check : Copy" class="h-3.5 w-3.5" aria-hidden="true" />
        {{ copied ? 'Tersalin' : 'Salin' }}
      </button>
    </div>

    <p
      v-if="failedCount > 0"
      class="mt-2 inline-flex items-start gap-2 rounded-md border border-amber-300 bg-amber-50 px-3 py-2 text-xs text-amber-900 dark:border-amber-900 dark:bg-amber-950 dark:text-amber-200"
    >
      <AlertTriangle class="mt-0.5 h-3.5 w-3.5 shrink-0" aria-hidden="true" />
      <span>
        {{ failedCount }} bagian gagal ditranskrip, jadi transkrip ini tidak lengkap.
      </span>
    </p>

    <p v-if="chunks.length === 0" class="mt-3 text-sm text-slate-500 dark:text-slate-400">
      Tidak ada transkrip untuk rapat ini.
    </p>

    <div v-else class="mt-3 space-y-3">
      <div
        v-for="chunk in chunks"
        :key="chunk.id"
        class="grid grid-cols-[4rem_1fr] gap-3 border-b border-slate-100 pb-3 last:border-0 dark:border-slate-800"
      >
        <span class="pt-0.5 font-mono text-xs tabular-nums text-slate-400 dark:text-slate-500">
          {{ formatDuration(chunk.start_time_sec) }}
        </span>

        <p v-if="chunk.status === 'COMPLETED' && chunk.transcript_text" class="text-sm leading-relaxed">
          {{ chunk.transcript_text }}
        </p>
        <p
          v-else-if="chunk.status === 'FAILED'"
          class="text-sm text-red-600 dark:text-red-400"
        >
          Gagal ditranskrip{{ chunk.error_message ? `: ${chunk.error_message}` : '' }}
        </p>
        <p v-else class="text-sm italic text-slate-400 dark:text-slate-500">(hening)</p>
      </div>
    </div>
  </section>
</template>
