<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { AlertTriangle, ArrowLeft, Check, FileDown, FileWarning, Layers, Loader2, Pencil, RefreshCw, Square, Trash2 } from 'lucide-vue-next'

import ActionItems from '@/components/ActionItems.vue'
import MeetingSummary from '@/components/MeetingSummary.vue'
import StatusBadge from '@/components/StatusBadge.vue'
import TranscriptViewer from '@/components/TranscriptViewer.vue'
import { useSSE } from '@/composables/useSSE'
import { api } from '@/services/api'
import { useMeetingStore } from '@/stores/meeting'
import { formatDate, formatDurationShort } from '@/utils/format'
import type { MeetingStatusEvent } from '@/types/api'

const route = useRoute()
const router = useRouter()
const store = useMeetingStore()
const sse = useSSE()

const meetingId = computed(() => String(route.params.id))
const meeting = computed(() => store.current)

const editingTitle = ref(false)
const titleDraft = ref('')

onMounted(load)
watch(meetingId, load)

async function load() {
  const detail = await store.fetchMeeting(meetingId.value)
  if (!detail) return

  titleDraft.value = detail.title

  // A meeting still being processed will finish while this page is open;
  // watch for it rather than making the user reload.
  if (detail.status === 'PROCESSING' || detail.status === 'RECORDING') {
    sse.connect(api.streamUrl(detail.id), {
      onTranscript: () => void store.refreshCurrent(),
      onMeetingStatus: (data) => {
        const event = data as MeetingStatusEvent
        void store.refreshCurrent()
        if (event.status === 'COMPLETED' || event.status === 'FAILED') sse.disconnect()
      },
      onError: (data) => {
        // A failed regeneration reports why, rather than silently reverting.
        const event = data as { scope?: string; message?: string }
        if (event.scope === 'summary') summaryError.value = event.message ?? null
      },
    })
  }
}

async function saveTitle() {
  const next = titleDraft.value.trim()
  if (!next || !meeting.value || next === meeting.value.title) {
    editingTitle.value = false
    titleDraft.value = meeting.value?.title ?? ''
    return
  }
  await store.updateMeeting(meetingId.value, { title: next })
  editingTitle.value = false
}

/**
 * Toggle an action item.
 *
 * `completed` is local state the user owns (PRD §16), so the whole notes
 * object is written back with just that field changed.
 */
async function toggleAction(index: number) {
  const summary = meeting.value?.summary_data
  if (!summary) return

  const next = {
    ...summary,
    action_items: summary.action_items.map((item, i) =>
      i === index ? { ...item, completed: !item.completed } : item,
    ),
  }
  await store.updateMeeting(meetingId.value, { summary_data: next })
}

const includeTranscript = ref(false)
const summaryError = ref<string | null>(null)

const sourceCount = computed(() => meeting.value?.source_meeting_ids?.length ?? 0)

// Regenerating is only possible once recording has stopped and there is
// something to summarize.
const canResummarize = computed(() => {
  const m = meeting.value
  if (!m) return false
  if (m.status === 'RECORDING' || m.status === 'PROCESSING') return false
  return Boolean(m.raw_transcript) || m.transcript_chunks.some((c) => c.transcript_text.trim())
})

async function resummarize() {
  summaryError.value = null
  const ok = await store.resummarize(meetingId.value)
  if (!ok) {
    summaryError.value = store.error
    return
  }
  // The run happens on the server; follow it over SSE like the first pass.
  await load()
}

/**
 * Export the notes as PDF through the browser's print pipeline.
 *
 * No PDF library: the browser already renders this page with correct
 * Indonesian glyphs and selectable text, and `@media print` in style.css
 * turns it into a document. The reader picks "Save as PDF" in the dialog.
 */
function exportPdf() {
  // Chrome and Firefox seed the suggested filename from document.title.
  const original = document.title
  const slug = (meeting.value?.title ?? 'Notulen').replace(/[\/:*?"<>|]/g, '-').trim()
  document.title = `Notulen - ${slug}`

  // Restoring on afterprint rather than straight after print(), because the
  // dialog is modal in some browsers and asynchronous in others.
  const restore = () => {
    document.title = original
    window.removeEventListener('afterprint', restore)
  }
  window.addEventListener('afterprint', restore)

  window.print()
}

async function finishRecording() {
  summaryError.value = null
  if (!(await store.finishRecording(meetingId.value))) {
    summaryError.value = store.error
    return
  }
  await load()
}

async function remove() {
  if (!window.confirm('Hapus rapat ini beserta transkrip dan notulennya?')) return
  if (await store.deleteMeeting(meetingId.value)) {
    void router.push('/')
  }
}
</script>

<template>
  <section>
    <RouterLink
      to="/"
      class="no-print inline-flex items-center gap-1.5 text-sm text-slate-500 hover:text-slate-900 dark:text-slate-400 dark:hover:text-slate-100"
    >
      <ArrowLeft class="h-4 w-4" aria-hidden="true" />
      Semua rapat
    </RouterLink>

    <p v-if="store.loading && !meeting" class="mt-6 text-sm text-slate-500">Memuat…</p>

    <p
      v-else-if="store.error && !meeting"
      role="alert"
      class="mt-6 rounded-md border border-red-300 bg-red-50 px-4 py-3 text-sm text-red-800 dark:border-red-900 dark:bg-red-950 dark:text-red-200"
    >
      {{ store.error }}
    </p>

    <article v-else-if="meeting" class="mt-4">
      <header class="border-b border-slate-200 pb-5 dark:border-slate-800">
        <div class="flex items-start justify-between gap-4">
          <div class="min-w-0 flex-1">
            <div v-if="editingTitle" class="flex items-center gap-2">
              <input
                v-model="titleDraft"
                type="text"
                aria-label="Judul rapat"
                class="w-full rounded-md border border-slate-300 px-3 py-1.5 text-lg font-semibold dark:border-slate-700 dark:bg-slate-900"
                @keyup.enter="saveTitle"
                @keyup.esc="editingTitle = false"
              />
              <button
                type="button"
                class="rounded-md bg-sky-600 p-2 text-white hover:bg-sky-700 disabled:opacity-50"
                :disabled="store.saving"
                aria-label="Simpan judul"
                @click="saveTitle"
              >
                <Loader2 v-if="store.saving" class="h-4 w-4 animate-spin" aria-hidden="true" />
                <Check v-else class="h-4 w-4" aria-hidden="true" />
              </button>
            </div>

            <h2 v-else class="group flex items-center gap-2 text-lg font-semibold">
              <span class="min-w-0 break-words">{{ meeting.title }}</span>
              <button
                type="button"
                class="no-print rounded p-1 text-slate-400 opacity-0 hover:text-slate-700 focus-visible:opacity-100 group-hover:opacity-100 dark:hover:text-slate-200"
                aria-label="Ubah judul"
                @click="editingTitle = true; titleDraft = meeting.title"
              >
                <Pencil class="h-3.5 w-3.5" aria-hidden="true" />
              </button>
            </h2>

            <p class="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-slate-500 dark:text-slate-400">
              <span>{{ formatDate(meeting.created_at) }}</span>
              <span v-if="meeting.duration_seconds > 0">
                {{ formatDurationShort(meeting.duration_seconds) }}
              </span>
              <span v-if="meeting.language_used" class="uppercase">{{ meeting.language_used }}</span>
              <StatusBadge :status="meeting.status" />
              <span v-if="sourceCount" class="inline-flex items-center gap-1">
                <Layers class="h-3 w-3" aria-hidden="true" />
                Gabungan dari {{ sourceCount }} diskusi
              </span>
            </p>
          </div>

          <div class="no-print flex shrink-0 items-center gap-2">
            <button
              v-if="meeting.summary_data && canResummarize"
              type="button"
              class="inline-flex items-center gap-1.5 rounded-md border border-slate-300 px-2.5 py-2 text-xs font-medium hover:bg-slate-50 disabled:opacity-50 dark:border-slate-700 dark:hover:bg-slate-900"
              :disabled="store.resummarizing"
              title="Buat ulang notulen dari transkrip"
              @click="resummarize"
            >
              <RefreshCw
                class="h-4 w-4"
                :class="{ 'animate-spin': store.resummarizing }"
                aria-hidden="true"
              />
              Buat ulang
            </button>

            <button
              v-if="meeting.summary_data || meeting.transcript_chunks.length"
              type="button"
              class="inline-flex items-center gap-1.5 rounded-md border border-slate-300 px-2.5 py-2 text-xs font-medium hover:bg-slate-50 dark:border-slate-700 dark:hover:bg-slate-900"
              @click="exportPdf"
            >
              <FileDown class="h-4 w-4" aria-hidden="true" />
              PDF
            </button>

            <label
              v-if="meeting.transcript_chunks.length"
              class="inline-flex cursor-pointer items-center gap-1.5 text-xs text-slate-500 dark:text-slate-400"
            >
              <input v-model="includeTranscript" type="checkbox" class="h-3.5 w-3.5 accent-sky-600" />
              transkrip
            </label>

            <button
              type="button"
              class="rounded-md border border-slate-300 p-2 text-slate-500 hover:bg-red-50 hover:text-red-600 dark:border-slate-700 dark:hover:bg-red-950"
              aria-label="Hapus rapat"
              @click="remove"
            >
              <Trash2 class="h-4 w-4" aria-hidden="true" />
            </button>
          </div>
        </div>
      </header>

      <div
        v-if="meeting.status === 'RECORDING'"
        class="no-print mt-5 flex flex-wrap items-start gap-3 rounded-md border border-amber-300 bg-amber-50 px-4 py-3 text-sm text-amber-900 dark:border-amber-900 dark:bg-amber-950 dark:text-amber-200"
      >
        <AlertTriangle class="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
        <div class="min-w-0 flex-1">
          <p class="font-medium">Rekaman ini belum diselesaikan.</p>
          <p class="mt-1">
            Transkripnya sudah tersimpan, tapi notulennya belum dibuat. Ini terjadi kalau halaman
            rekaman ditinggalkan sebelum ditekan berhenti.
          </p>
          <button
            type="button"
            class="mt-3 inline-flex items-center gap-2 rounded-md bg-amber-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-amber-700 disabled:opacity-50"
            :disabled="store.finishing"
            @click="finishRecording"
          >
            <Loader2 v-if="store.finishing" class="h-4 w-4 animate-spin" aria-hidden="true" />
            <Square v-else class="h-4 w-4" aria-hidden="true" />
            {{ store.finishing ? 'Menyelesaikan…' : 'Selesaikan & buat notulen' }}
          </button>
        </div>
      </div>

      <div
        v-if="meeting.status === 'PROCESSING'"
        class="no-print mt-5 inline-flex items-center gap-2 rounded-md border border-amber-300 bg-amber-50 px-4 py-3 text-sm text-amber-900 dark:border-amber-900 dark:bg-amber-950 dark:text-amber-200"
      >
        <Loader2 class="h-4 w-4 animate-spin" aria-hidden="true" />
        Menyusun notulen… halaman ini akan memperbarui sendiri.
      </div>

      <p
        v-if="summaryError"
        role="alert"
        class="no-print mt-5 flex items-start gap-2 rounded-md border border-red-300 bg-red-50 px-4 py-3 text-sm text-red-800 dark:border-red-900 dark:bg-red-950 dark:text-red-200"
      >
        <AlertTriangle class="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
        <span>{{ summaryError }}</span>
      </p>

      <div class="mt-6 space-y-8">
        <MeetingSummary v-if="meeting.summary_data" :summary="meeting.summary_data" />

        <div
          v-else-if="meeting.status === 'COMPLETED' || meeting.status === 'FAILED'"
          class="flex items-start gap-3 rounded-md border border-slate-200 px-4 py-3 dark:border-slate-800"
        >
          <FileWarning class="mt-0.5 h-4 w-4 shrink-0 text-slate-400" aria-hidden="true" />
          <div class="min-w-0 flex-1">
            <p class="text-sm font-medium">Notulen belum tersedia.</p>
            <p class="mt-1 text-sm text-slate-500 dark:text-slate-400">
              <template v-if="canResummarize">
                Pembuatan notulen gagal atau belum dijalankan. Transkripnya masih utuh, jadi bisa
                dicoba lagi.
              </template>
              <template v-else>
                Transkripnya terlalu pendek untuk diringkas.
              </template>
            </p>

            <button
              v-if="canResummarize"
              type="button"
              class="no-print mt-3 inline-flex items-center gap-2 rounded-md bg-sky-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-sky-700 disabled:opacity-50"
              :disabled="store.resummarizing"
              @click="resummarize"
            >
              <RefreshCw
                class="h-4 w-4"
                :class="{ 'animate-spin': store.resummarizing }"
                aria-hidden="true"
              />
              {{ store.resummarizing ? 'Memulai…' : 'Buat notulen sekarang' }}
            </button>
          </div>
        </div>

        <ActionItems
          v-if="meeting.summary_data"
          :items="meeting.summary_data.action_items"
          @toggle="toggleAction"
        />

        <div
          class="print-page-break"
          :data-print-hide="includeTranscript ? undefined : true"
        >
          <TranscriptViewer :chunks="meeting.transcript_chunks" />
        </div>
      </div>
    </article>
  </section>
</template>
