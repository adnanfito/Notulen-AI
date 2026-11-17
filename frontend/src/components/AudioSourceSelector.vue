<script setup lang="ts">
import { computed } from 'vue'
import { Mic, MonitorSpeaker, Radio } from 'lucide-vue-next'

import { supportsSystemAudio } from '@/composables/useAudioRecorder'
import type { AudioSource } from '@/composables/useAudioRecorder'

const model = defineModel<AudioSource>({ required: true })
defineProps<{ disabled?: boolean }>()

const systemAvailable = supportsSystemAudio()

const options = computed(() => [
  {
    value: 'MIC_ONLY' as const,
    label: 'Mikrofon',
    icon: Mic,
    description: 'Merekam suara dari mikrofon saja.',
    available: true,
  },
  {
    value: 'SYSTEM_ONLY' as const,
    label: 'Audio Sistem',
    icon: MonitorSpeaker,
    description: 'Merekam suara dari tab atau aplikasi. Centang "bagikan audio" di dialog.',
    available: systemAvailable,
  },
  {
    value: 'FULL_MEETING' as const,
    label: 'Mikrofon + Audio Sistem',
    icon: Radio,
    description: 'Merekam keduanya sekaligus. Pakai headset agar suara tidak terekam dua kali.',
    available: systemAvailable,
  },
])
</script>

<template>
  <fieldset :disabled="disabled">
    <legend class="text-sm font-medium">Sumber audio</legend>

    <div class="mt-3 space-y-2" role="radiogroup" aria-label="Sumber audio">
      <label
        v-for="option in options"
        :key="option.value"
        class="flex cursor-pointer items-start gap-3 rounded-md border p-3 transition"
        :class="[
          model === option.value
            ? 'border-sky-500 bg-sky-50 dark:bg-sky-950/40'
            : 'border-slate-200 hover:bg-slate-50 dark:border-slate-800 dark:hover:bg-slate-900',
          (!option.available || disabled) && 'cursor-not-allowed opacity-50',
        ]"
      >
        <input
          v-model="model"
          type="radio"
          name="audio-source"
          :value="option.value"
          :disabled="!option.available || disabled"
          class="mt-0.5 h-4 w-4 shrink-0 accent-sky-600"
        />
        <component :is="option.icon" class="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
        <span class="min-w-0">
          <span class="block text-sm font-medium">{{ option.label }}</span>
          <span class="mt-0.5 block text-xs text-slate-500 dark:text-slate-400">
            {{ option.description }}
            <template v-if="!option.available"> Tidak didukung browser ini.</template>
          </span>
        </span>
      </label>
    </div>
  </fieldset>
</template>
