<script setup lang="ts">
import { RouterLink, RouterView } from 'vue-router'
import { AudioLines } from 'lucide-vue-next'

import RecordingIndicator from '@/components/RecordingIndicator.vue'
</script>

<template>
  <div class="min-h-full">
    <header class="no-print border-b border-slate-200 dark:border-slate-800">
      <div class="mx-auto flex max-w-4xl items-center justify-between px-6 py-4">
        <RouterLink to="/" class="inline-flex items-center gap-2 rounded text-base font-semibold">
          <AudioLines class="h-5 w-5 text-sky-600" aria-hidden="true" />
          Notulen AI
        </RouterLink>
        <div class="flex items-center gap-3">
          <RecordingIndicator />
          <span class="text-xs text-slate-500 dark:text-slate-400">Self-hosted</span>
        </div>
      </div>
    </header>

    <main class="mx-auto max-w-4xl px-6 py-8">
      <RouterView />
    </main>
  </div>
</template>
