<script setup lang="ts">
import { computed } from 'vue'
import { CheckCircle2, Circle, Loader2, XCircle } from 'lucide-vue-next'

import { STATUS_LABELS } from '@/utils/format'
import type { MeetingStatus } from '@/types/api'

const props = defineProps<{ status: MeetingStatus }>()

// Status carries an icon and a word, never colour alone (PRD §35).
const icon = computed(() => {
  switch (props.status) {
    case 'COMPLETED':
      return CheckCircle2
    case 'PROCESSING':
      return Loader2
    case 'FAILED':
      return XCircle
    default:
      return Circle
  }
})

const tone = computed(() => {
  switch (props.status) {
    case 'COMPLETED':
      return 'text-emerald-700 dark:text-emerald-400'
    case 'PROCESSING':
      return 'text-amber-700 dark:text-amber-400'
    case 'FAILED':
      return 'text-red-700 dark:text-red-400'
    default:
      return 'text-sky-700 dark:text-sky-400'
  }
})

const label = computed(() => STATUS_LABELS[props.status] ?? props.status)
</script>

<template>
  <span class="inline-flex items-center gap-1.5 text-xs font-medium" :class="tone">
    <component
      :is="icon"
      class="h-3.5 w-3.5"
      :class="{ 'animate-spin': status === 'PROCESSING' }"
      aria-hidden="true"
    />
    {{ label }}
  </span>
</template>
