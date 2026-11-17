import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

import { ApiError, api } from '@/services/api'
import type { MeetingDetail, MeetingListItem, SummaryData } from '@/types/api'

function describe(err: unknown): string {
  return err instanceof ApiError ? err.message : 'Terjadi kesalahan tak terduga.'
}

export const useMeetingStore = defineStore('meeting', () => {
  const meetings = ref<MeetingListItem[]>([])
  const total = ref(0)
  const page = ref(1)
  const limit = ref(20)
  const search = ref('')

  const current = ref<MeetingDetail | null>(null)

  const loading = ref(false)
  const saving = ref(false)
  const error = ref<string | null>(null)

  const totalPages = computed(() => Math.max(1, Math.ceil(total.value / limit.value)))

  async function fetchMeetings(opts: { q?: string; page?: number } = {}) {
    loading.value = true
    error.value = null

    if (opts.q !== undefined) search.value = opts.q
    // Any new search starts from page one; staying on page 4 of the previous
    // result set would show an empty screen.
    page.value = opts.page ?? 1

    try {
      const result = await api.listMeetings({ q: search.value, page: page.value, limit: limit.value })
      meetings.value = result.items
      total.value = result.total
    } catch (err) {
      error.value = describe(err)
      meetings.value = []
      total.value = 0
    } finally {
      loading.value = false
    }
  }

  async function fetchMeeting(id: string) {
    loading.value = true
    error.value = null
    try {
      current.value = await api.getMeeting(id)
      return current.value
    } catch (err) {
      error.value = describe(err)
      current.value = null
      return null
    } finally {
      loading.value = false
    }
  }

  /** Refetch without the loading flag, so live updates do not blank the page. */
  async function refreshCurrent() {
    if (!current.value) return
    try {
      current.value = await api.getMeeting(current.value.id)
    } catch {
      // A failed background refresh is not worth interrupting the user for.
    }
  }

  async function createMeeting(title?: string) {
    error.value = null
    try {
      return await api.createMeeting(title)
    } catch (err) {
      error.value = describe(err)
      return null
    }
  }

  async function updateMeeting(id: string, patch: { title?: string; summary_data?: SummaryData }) {
    saving.value = true
    error.value = null
    try {
      const updated = await api.updateMeeting(id, patch)
      if (current.value?.id === id) {
        current.value = updated
      }
      const row = meetings.value.find((m) => m.id === id)
      if (row && patch.title) row.title = patch.title
      return updated
    } catch (err) {
      error.value = describe(err)
      return null
    } finally {
      saving.value = false
    }
  }

  const combining = ref(false)

  async function combineMeetings(ids: string[], title?: string) {
    combining.value = true
    error.value = null
    try {
      return await api.combineMeetings(ids, title)
    } catch (err) {
      error.value = describe(err)
      return null
    } finally {
      combining.value = false
    }
  }

  const finishing = ref(false)

  /**
   * Finish a meeting that was left in RECORDING.
   *
   * Navigating away used to stop the microphone without telling the backend,
   * stranding the meeting with its chunks transcribed but never combined.
   * This runs the same stop the recorder would have run.
   */
  async function finishRecording(id: string) {
    finishing.value = true
    error.value = null
    try {
      const detail = current.value
      await api.stopMeeting(id, detail?.duration_seconds ?? 0)
      await refreshCurrent()
      return true
    } catch (err) {
      error.value = describe(err)
      return false
    } finally {
      finishing.value = false
    }
  }

  const resummarizing = ref(false)

  async function resummarize(id: string) {
    resummarizing.value = true
    error.value = null
    try {
      await api.resummarize(id)
      // Status flips to PROCESSING; the page follows the rest over SSE.
      await refreshCurrent()
      return true
    } catch (err) {
      error.value = describe(err)
      return false
    } finally {
      resummarizing.value = false
    }
  }

  async function deleteMeeting(id: string) {
    error.value = null
    try {
      await api.deleteMeeting(id)
      meetings.value = meetings.value.filter((m) => m.id !== id)
      total.value = Math.max(0, total.value - 1)
      if (current.value?.id === id) current.value = null
      return true
    } catch (err) {
      error.value = describe(err)
      return false
    }
  }

  return {
    meetings,
    total,
    page,
    limit,
    search,
    current,
    loading,
    saving,
    resummarizing,
    finishing,
    combining,
    error,
    totalPages,
    fetchMeetings,
    fetchMeeting,
    refreshCurrent,
    createMeeting,
    updateMeeting,
    resummarize,
    finishRecording,
    combineMeetings,
    deleteMeeting,
  }
})
