<script setup lang="ts">
import { RouterLink } from 'vue-router'
import { FileText, Layers, Trash2 } from 'lucide-vue-next'

import StatusBadge from './StatusBadge.vue'
import { formatDate, formatDurationShort } from '@/utils/format'
import type { MeetingListItem } from '@/types/api'

defineProps<{
  meeting: MeetingListItem
  selectable?: boolean
  selected?: boolean
  /** Selection is only meaningful for a meeting that has a transcript. */
  selectDisabled?: boolean
}>()

const emit = defineEmits<{
  delete: [id: string]
  toggle: [id: string]
}>()
</script>

<template>
  <li
    class="group flex items-center gap-4 border-b border-slate-200 px-4 py-3 last:border-0 hover:bg-slate-50 dark:border-slate-800 dark:hover:bg-slate-900/50"
  >
    <input
      v-if="selectable"
      type="checkbox"
      class="h-4 w-4 shrink-0 accent-sky-600 disabled:opacity-40"
      :checked="selected"
      :disabled="selectDisabled"
      :aria-label="`Pilih ${meeting.title}`"
      @change="emit('toggle', meeting.id)"
    />

    <RouterLink
      :to="`/meetings/${meeting.id}`"
      class="min-w-0 flex-1 rounded focus-visible:outline-none"
    >
      <p class="truncate text-sm font-medium">{{ meeting.title }}</p>
      <p class="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-slate-500 dark:text-slate-400">
        <span>{{ formatDate(meeting.created_at) }}</span>
        <span v-if="meeting.duration_seconds > 0">{{ formatDurationShort(meeting.duration_seconds) }}</span>
        <span v-if="meeting.language_used" class="uppercase">{{ meeting.language_used }}</span>
        <span v-if="meeting.has_summary" class="inline-flex items-center gap-1">
          <FileText class="h-3 w-3" aria-hidden="true" />
          Notulen
        </span>
        <span v-if="meeting.is_combined" class="inline-flex items-center gap-1">
          <Layers class="h-3 w-3" aria-hidden="true" />
          Gabungan
        </span>
      </p>
    </RouterLink>

    <StatusBadge :status="meeting.status" />

    <button
      type="button"
      class="rounded p-1.5 text-slate-400 opacity-0 transition hover:bg-red-50 hover:text-red-600 focus-visible:opacity-100 group-hover:opacity-100 dark:hover:bg-red-950"
      :aria-label="`Hapus rapat ${meeting.title}`"
      @click="emit('delete', meeting.id)"
    >
      <Trash2 class="h-4 w-4" aria-hidden="true" />
    </button>
  </li>
</template>
