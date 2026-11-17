<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'

import MeetingDetailView from './MeetingDetailView.vue'
import RecordingView from './RecordingView.vue'
import { useRecordingStore } from '@/stores/recording'

/**
 * One URL, two faces.
 *
 * `/meetings/:id` is the meeting's address whether it is being recorded right
 * now or was finished last week. Returning to a recording in progress has to
 * show the live view — not the finished-meeting page telling the user their
 * recording was never completed.
 */
const route = useRoute()
const recording = useRecordingStore()

const isLive = computed(() => recording.isLive(String(route.params.id)))
</script>

<template>
  <RecordingView v-if="isLive" />
  <MeetingDetailView v-else />
</template>
