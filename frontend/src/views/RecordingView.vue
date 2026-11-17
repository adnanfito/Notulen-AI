<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import { AlertTriangle, Circle, Loader2, Pause, Play, Square } from 'lucide-vue-next'

import AudioSourceSelector from '@/components/AudioSourceSelector.vue'
import LiveTranscript from '@/components/LiveTranscript.vue'
import { isRecordingSupported } from '@/composables/useAudioRecorder'
import { useRecordingStore } from '@/stores/recording'
import { formatDuration } from '@/utils/format'

/**
 * The setup form and the live recording view.
 *
 * It owns neither the recorder nor the event stream — the store does, so a
 * recording survives the user navigating away and coming back. This component
 * only renders that state and drives it.
 */
const router = useRouter()
const store = useRecordingStore()

const supported = isRecordingSupported()
const starting = ref(false)
const title = ref('')

const elapsed = computed(() => formatDuration(store.recorder.elapsedSeconds))

// Over a running recording, "Rapat baru" says nothing about which meeting this
// is.
const heading = computed(() => {
  if (!store.active && !store.stopping) return 'Rapat baru'
  return store.meetingTitle.trim() || 'Rapat tanpa judul'
})

const connectionLabel = computed(() => {
  switch (store.connection) {
    case 'open':
      return 'Tersambung'
    case 'connecting':
      return 'Menyambung ulang…'
    case 'closed':
      return 'Terputus'
    default:
      return '—'
  }
})

async function start() {
  if (starting.value || store.active) return

  starting.value = true
  try {
    const id = await store.begin(title.value, store.source)
    if (!id) return

    // The address bar names the meeting from here on. replace, not push: the
    // setup form should not sit in history behind a running recording.
    void router.replace(`/meetings/${id}`)
  } finally {
    starting.value = false
  }
}

async function stop() {
  const id = await store.finish()
  if (id) void router.replace(`/meetings/${id}`)
}
</script>

<template>
  <section>
    <h2 class="text-lg font-semibold">{{ heading }}</h2>

    <div
      v-if="!supported"
      role="alert"
      class="mt-6 rounded-md border border-amber-300 bg-amber-50 px-4 py-3 text-sm text-amber-900 dark:border-amber-900 dark:bg-amber-950 dark:text-amber-200"
    >
      <p class="font-medium">Browser ini tidak mendukung perekaman audio.</p>
      <p class="mt-1">Gunakan Chrome, Edge, atau browser berbasis Chromium lainnya.</p>
    </div>

    <template v-else>
      <!-- Setup -->
      <div v-if="!store.active && !store.stopping" class="mt-6 max-w-xl space-y-6">
        <div>
          <label for="meeting-title" class="block text-sm font-medium">Judul (opsional)</label>
          <input
            id="meeting-title"
            v-model="title"
            type="text"
            placeholder="Mis. Sinkronisasi Mingguan Engineering"
            class="mt-2 w-full rounded-md border border-slate-300 px-3 py-2 text-sm dark:border-slate-700 dark:bg-slate-900"
          />
          <p class="mt-1.5 text-xs text-slate-500 dark:text-slate-400">
            Kosongkan saja — judul akan dibuat otomatis dari isi rapat.
          </p>
        </div>

        <AudioSourceSelector v-model="store.source" />

        <button
          type="button"
          class="inline-flex items-center gap-2 rounded-md bg-sky-600 px-4 py-2 text-sm font-medium text-white hover:bg-sky-700 disabled:opacity-50"
          :disabled="starting"
          @click="start"
        >
          <Loader2 v-if="starting" class="h-4 w-4 animate-spin" aria-hidden="true" />
          <Circle v-else class="h-4 w-4 fill-current" aria-hidden="true" />
          {{ starting ? 'Menyiapkan…' : 'Mulai merekam' }}
        </button>
      </div>

      <!-- Recording -->
      <div v-else class="mt-6 space-y-5">
        <div class="flex flex-wrap items-center justify-between gap-4">
          <div class="flex items-center gap-3">
            <span
              class="inline-flex h-2.5 w-2.5 rounded-full"
              :class="
                !store.active
                  ? 'bg-slate-400'
                  : store.paused
                    ? 'bg-amber-500'
                    : 'animate-pulse bg-red-600'
              "
              aria-hidden="true"
            />
            <p class="font-mono text-2xl tabular-nums" aria-label="Durasi rekaman">{{ elapsed }}</p>
            <span class="text-xs text-slate-500 dark:text-slate-400">
              <template v-if="!store.active">Menyelesaikan…</template>
              <template v-else-if="store.paused">Dijeda</template>
              <template v-else>Merekam</template>
            </span>
          </div>

          <div class="flex items-center gap-2">
            <button
              v-if="store.active"
              type="button"
              class="inline-flex items-center gap-2 rounded-md border border-slate-300 px-4 py-2 text-sm font-medium hover:bg-slate-50 disabled:opacity-50 dark:border-slate-700 dark:hover:bg-slate-900"
              :disabled="store.stopping"
              @click="store.togglePause()"
            >
              <Play v-if="store.paused" class="h-4 w-4" aria-hidden="true" />
              <Pause v-else class="h-4 w-4" aria-hidden="true" />
              {{ store.paused ? 'Lanjutkan' : 'Jeda' }}
            </button>

            <button
              type="button"
              class="inline-flex items-center gap-2 rounded-md border border-slate-300 px-4 py-2 text-sm font-medium hover:bg-slate-50 disabled:opacity-50 dark:border-slate-700 dark:hover:bg-slate-900"
              :disabled="store.stopping"
              @click="stop()"
            >
              <Loader2 v-if="store.stopping" class="h-4 w-4 animate-spin" aria-hidden="true" />
              <Square v-else class="h-4 w-4" aria-hidden="true" />
              {{ store.stopping ? 'Membuat notulen…' : 'Hentikan rekaman' }}
            </button>
          </div>
        </div>

        <dl class="grid grid-cols-2 gap-3 text-xs sm:grid-cols-4">
          <div class="rounded-md border border-slate-200 px-3 py-2 dark:border-slate-800">
            <dt class="text-slate-500 dark:text-slate-400">Terkirim</dt>
            <dd class="mt-0.5 font-medium tabular-nums">{{ store.uploadedCount }}</dd>
          </div>
          <div class="rounded-md border border-slate-200 px-3 py-2 dark:border-slate-800">
            <dt class="text-slate-500 dark:text-slate-400">Ditranskrip</dt>
            <dd class="mt-0.5 font-medium tabular-nums">{{ store.processedCount }}</dd>
          </div>
          <div class="rounded-md border border-slate-200 px-3 py-2 dark:border-slate-800">
            <dt class="text-slate-500 dark:text-slate-400">Gagal</dt>
            <dd class="mt-0.5 font-medium tabular-nums">{{ store.failedCount }}</dd>
          </div>
          <div class="rounded-md border border-slate-200 px-3 py-2 dark:border-slate-800">
            <dt class="text-slate-500 dark:text-slate-400">Koneksi</dt>
            <dd class="mt-0.5 font-medium">{{ connectionLabel }}</dd>
          </div>
        </dl>

        <div>
          <h3 class="text-sm font-medium">Transkrip langsung</h3>
          <LiveTranscript class="mt-2" :chunks="store.orderedChunks" :recording="store.active" />
          <p class="mt-2 text-xs text-slate-500 dark:text-slate-400">
            <template v-if="store.paused">
              Rekaman dijeda. Bagian yang sudah terekam tetap aman dan sedang ditranskrip.
            </template>
            <template v-else>
              Transkrip muncul tiap {{ store.chunkSeconds }} detik. Kamu bisa berpindah halaman —
              rekaman tetap berjalan.
            </template>
          </p>
        </div>
      </div>
    </template>

    <p
      v-if="store.error"
      role="alert"
      class="mt-6 rounded-md border border-red-300 bg-red-50 px-4 py-3 text-sm text-red-800 dark:border-red-900 dark:bg-red-950 dark:text-red-200"
    >
      <span class="inline-flex items-start gap-2">
        <AlertTriangle class="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
        <span>
          {{ store.error }}
          <span v-if="store.hint" class="mt-1 block opacity-90">{{ store.hint }}</span>
        </span>
      </span>
    </p>
  </section>
</template>
