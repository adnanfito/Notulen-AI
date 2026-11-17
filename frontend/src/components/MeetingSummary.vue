<script setup lang="ts">
import { ref } from 'vue'
import { ChevronDown } from 'lucide-vue-next'

import type { SummaryData } from '@/types/api'

defineProps<{ summary: SummaryData }>()

const openTopics = ref<Set<number>>(new Set([0]))

function toggle(index: number) {
  const next = new Set(openTopics.value)
  next.has(index) ? next.delete(index) : next.add(index)
  openTopics.value = next
}
</script>

<template>
  <div class="space-y-8">
    <section aria-labelledby="summary-heading">
      <h3 id="summary-heading" class="text-sm font-semibold">Ringkasan</h3>
      <p class="mt-2 text-sm leading-relaxed text-slate-700 dark:text-slate-300">
        {{ summary.executive_summary }}
      </p>
    </section>

    <section v-if="summary.key_decisions.length" aria-labelledby="decisions-heading">
      <h3 id="decisions-heading" class="text-sm font-semibold">Keputusan</h3>
      <ul class="mt-2 space-y-1.5">
        <li
          v-for="(decision, i) in summary.key_decisions"
          :key="i"
          class="flex gap-2 text-sm text-slate-700 dark:text-slate-300"
        >
          <span class="mt-2 h-1 w-1 shrink-0 rounded-full bg-slate-400" aria-hidden="true" />
          {{ decision }}
        </li>
      </ul>
    </section>

    <section v-if="summary.main_topics.length" aria-labelledby="topics-heading">
      <h3 id="topics-heading" class="text-sm font-semibold">Topik</h3>
      <div class="mt-2 divide-y divide-slate-200 rounded-md border border-slate-200 dark:divide-slate-800 dark:border-slate-800">
        <div v-for="(topic, i) in summary.main_topics" :key="i">
          <button
            type="button"
            class="print-as-heading flex w-full items-center justify-between gap-3 px-4 py-3 text-left text-sm font-medium hover:bg-slate-50 dark:hover:bg-slate-900"
            :aria-expanded="openTopics.has(i)"
            @click="toggle(i)"
          >
            {{ topic.topic_name }}
            <ChevronDown
              class="no-print h-4 w-4 shrink-0 transition-transform"
              :class="{ 'rotate-180': openTopics.has(i) }"
              aria-hidden="true"
            />
          </button>

          <ul
            v-show="openTopics.has(i)"
            class="print-expand space-y-1.5 px-4 pb-3"
          >
            <li
              v-for="(point, j) in topic.discussion_points"
              :key="j"
              class="flex gap-2 text-sm text-slate-700 dark:text-slate-300"
            >
              <span class="mt-2 h-1 w-1 shrink-0 rounded-full bg-slate-400" aria-hidden="true" />
              {{ point }}
            </li>
          </ul>
        </div>
      </div>
    </section>

    <section v-if="summary.tags.length" aria-labelledby="tags-heading">
      <h3 id="tags-heading" class="sr-only">Tag</h3>
      <ul class="flex flex-wrap gap-2">
        <li
          v-for="tag in summary.tags"
          :key="tag"
          class="rounded-full bg-slate-100 px-2.5 py-0.5 text-xs text-slate-600 dark:bg-slate-800 dark:text-slate-300"
        >
          {{ tag }}
        </li>
      </ul>
    </section>
  </div>
</template>
