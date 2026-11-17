<script setup lang="ts">
import { User, CalendarClock } from 'lucide-vue-next'

import type { ActionItem } from '@/types/api'

defineProps<{ items: ActionItem[] }>()
const emit = defineEmits<{ toggle: [index: number] }>()
</script>

<template>
  <section aria-labelledby="actions-heading">
    <h3 id="actions-heading" class="text-sm font-semibold">Tindak lanjut</h3>

    <p v-if="items.length === 0" class="mt-2 text-sm text-slate-500 dark:text-slate-400">
      Tidak ada tindak lanjut yang disebutkan.
    </p>

    <ul v-else class="mt-2 divide-y divide-slate-200 rounded-md border border-slate-200 dark:divide-slate-800 dark:border-slate-800">
      <li v-for="(item, i) in items" :key="i" class="flex items-start gap-3 px-4 py-3">
        <input
          :id="`action-${i}`"
          type="checkbox"
          :checked="item.completed"
          class="mt-0.5 h-4 w-4 shrink-0 accent-sky-600"
          @change="emit('toggle', i)"
        />
        <div class="min-w-0">
          <label
            :for="`action-${i}`"
            class="block cursor-pointer text-sm"
            :class="item.completed && 'text-slate-400 line-through dark:text-slate-500'"
          >
            {{ item.task }}
          </label>

          <p class="mt-1 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-slate-500 dark:text-slate-400">
            <span class="inline-flex items-center gap-1">
              <User class="h-3 w-3" aria-hidden="true" />
              <!-- null is the honest answer when the transcript never said
                   who owns this (PRD §18). -->
              {{ item.assignee ?? 'Belum ditentukan' }}
            </span>
            <span class="inline-flex items-center gap-1">
              <CalendarClock class="h-3 w-3" aria-hidden="true" />
              {{ item.deadline ?? 'Tanpa tenggat' }}
            </span>
          </p>
        </div>
      </li>
    </ul>
  </section>
</template>
