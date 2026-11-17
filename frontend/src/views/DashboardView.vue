<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import { Layers, Loader2, Plus, Search, X } from 'lucide-vue-next'

import MeetingCard from '@/components/MeetingCard.vue'
import { useMeetingStore } from '@/stores/meeting'
import { formatDate } from '@/utils/format'

const store = useMeetingStore()
const router = useRouter()
const query = ref('')

// Combining exists because one discussion often ends up split across
// recordings — the recording is stopped by accident, and the rest becomes a
// new meeting.
const selecting = ref(false)
const selected = ref<Set<string>>(new Set())

const selectedCount = computed(() => selected.value.size)

/** A meeting with no notes and no transcript has nothing to contribute. */
function canCombine(id: string) {
  const m = store.meetings.find((x) => x.id === id)
  if (!m) return false
  return m.status !== 'RECORDING' && m.status !== 'PROCESSING' && m.duration_seconds > 0
}

function toggle(id: string) {
  const next = new Set(selected.value)
  next.has(id) ? next.delete(id) : next.add(id)
  selected.value = next
}

function cancelSelection() {
  selecting.value = false
  selected.value = new Set()
}

async function combine() {
  // Chronological order is decided by the backend; this is only the preview.
  const chosen = store.meetings
    .filter((m) => selected.value.has(m.id))
    .sort((a, b) => a.created_at.localeCompare(b.created_at))

  // A split discussion keeps its title, so several rows read identically.
  // Without the timestamp the list gives the user nothing to check against.
  const names = chosen
    .map((m, i) => `${i + 1}. ${m.title} — ${formatDate(m.created_at)}`)
    .join(String.fromCharCode(10))
  const ok = window.confirm(
    [
      `Gabungkan ${chosen.length} diskusi ini jadi satu notulen?`,
      '',
      names,
      '',
      'Diskusi aslinya tidak diubah atau dihapus.',
    ].join(String.fromCharCode(10)),
  )
  if (!ok) return

  const result = await store.combineMeetings(chosen.map((m) => m.id))
  if (!result) return

  cancelSelection()
  void router.push(`/meetings/${result.meeting_id}`)
}

let debounce: number | undefined
watch(query, (value) => {
  // Search on every keystroke would hammer an ILIKE scan; a short pause is
  // enough to feel instant without that.
  window.clearTimeout(debounce)
  debounce = window.setTimeout(() => store.fetchMeetings({ q: value }), 300)
})

onMounted(() => store.fetchMeetings())

async function remove(id: string) {
  const meeting = store.meetings.find((m) => m.id === id)
  if (!window.confirm(`Hapus "${meeting?.title ?? 'rapat ini'}"? Transkrip dan notulennya ikut terhapus.`)) {
    return
  }
  await store.deleteMeeting(id)
}
</script>

<template>
  <section>
    <div class="flex flex-wrap items-center justify-between gap-4">
      <h2 class="text-lg font-semibold">Rapat</h2>

      <div class="flex items-center gap-2">
        <button
          v-if="!selecting && store.meetings.length > 1"
          type="button"
          class="inline-flex items-center gap-2 rounded-md border border-slate-300 px-3 py-2 text-sm font-medium hover:bg-slate-50 dark:border-slate-700 dark:hover:bg-slate-900"
          @click="selecting = true"
        >
          <Layers class="h-4 w-4" aria-hidden="true" />
          Gabungkan
        </button>

        <RouterLink
          to="/meetings/new"
          class="inline-flex items-center gap-2 rounded-md bg-sky-600 px-3 py-2 text-sm font-medium text-white hover:bg-sky-700"
        >
          <Plus class="h-4 w-4" aria-hidden="true" />
          Rapat baru
        </RouterLink>
      </div>
    </div>

    <div
      v-if="selecting"
      class="mt-5 flex flex-wrap items-center justify-between gap-3 rounded-md border border-sky-300 bg-sky-50 px-4 py-3 text-sm dark:border-sky-900 dark:bg-sky-950"
    >
      <p>
        <template v-if="selectedCount === 0">
          Pilih diskusi yang mau digabungkan jadi satu notulen.
        </template>
        <template v-else>
          <span class="font-medium">{{ selectedCount }} diskusi</span> dipilih.
          <span v-if="selectedCount < 2" class="text-slate-600 dark:text-slate-400">
            Pilih minimal dua.
          </span>
        </template>
      </p>

      <div class="flex items-center gap-2">
        <button
          type="button"
          class="inline-flex items-center gap-1.5 rounded-md px-2.5 py-1.5 text-sm hover:bg-white/60 dark:hover:bg-slate-900"
          @click="cancelSelection"
        >
          <X class="h-4 w-4" aria-hidden="true" />
          Batal
        </button>
        <button
          type="button"
          class="inline-flex items-center gap-2 rounded-md bg-sky-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-sky-700 disabled:opacity-50"
          :disabled="selectedCount < 2 || store.combining"
          @click="combine"
        >
          <Loader2 v-if="store.combining" class="h-4 w-4 animate-spin" aria-hidden="true" />
          <Layers v-else class="h-4 w-4" aria-hidden="true" />
          Gabungkan
        </button>
      </div>
    </div>

    <div class="relative mt-5">
      <Search
        class="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-slate-400"
        aria-hidden="true"
      />
      <input
        v-model="query"
        type="search"
        placeholder="Cari judul, transkrip, atau notulen…"
        aria-label="Cari rapat"
        class="w-full rounded-md border border-slate-300 py-2 pl-9 pr-3 text-sm dark:border-slate-700 dark:bg-slate-900"
      />
    </div>

    <p v-if="store.loading" class="mt-6 text-sm text-slate-500">Memuat…</p>

    <p
      v-else-if="store.error"
      role="alert"
      class="mt-6 rounded-md border border-red-300 bg-red-50 px-4 py-3 text-sm text-red-800 dark:border-red-900 dark:bg-red-950 dark:text-red-200"
    >
      {{ store.error }}
    </p>

    <div
      v-else-if="store.meetings.length === 0"
      class="mt-6 rounded-md border border-dashed border-slate-300 px-6 py-12 text-center dark:border-slate-700"
    >
      <p class="text-sm font-medium">
        {{ query ? 'Tidak ada rapat yang cocok.' : 'Belum ada rapat.' }}
      </p>
      <p class="mt-1 text-sm text-slate-500 dark:text-slate-400">
        {{ query ? 'Coba kata kunci lain.' : 'Mulai rapat pertamamu untuk melihatnya di sini.' }}
      </p>
    </div>

    <template v-else>
      <ul class="mt-5 rounded-md border border-slate-200 dark:border-slate-800">
        <MeetingCard
          v-for="meeting in store.meetings"
          :key="meeting.id"
          :meeting="meeting"
          :selectable="selecting"
          :selected="selected.has(meeting.id)"
          :select-disabled="!canCombine(meeting.id)"
          @delete="remove"
          @toggle="toggle"
        />
      </ul>

      <div v-if="store.totalPages > 1" class="mt-4 flex items-center justify-between text-sm">
        <button
          type="button"
          class="rounded-md border border-slate-300 px-3 py-1.5 disabled:opacity-40 dark:border-slate-700"
          :disabled="store.page <= 1"
          @click="store.fetchMeetings({ page: store.page - 1 })"
        >
          Sebelumnya
        </button>

        <span class="text-slate-500 dark:text-slate-400">
          Halaman {{ store.page }} dari {{ store.totalPages }} · {{ store.total }} rapat
        </span>

        <button
          type="button"
          class="rounded-md border border-slate-300 px-3 py-1.5 disabled:opacity-40 dark:border-slate-700"
          :disabled="store.page >= store.totalPages"
          @click="store.fetchMeetings({ page: store.page + 1 })"
        >
          Berikutnya
        </button>
      </div>
    </template>
  </section>
</template>
