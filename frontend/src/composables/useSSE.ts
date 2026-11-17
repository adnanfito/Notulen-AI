/**
 * Live meeting events over SSE, with reconnect.
 *
 * EventSource reconnects on its own, but a reconnect replays nothing: events
 * published while the tab was offline are simply gone. The database is the
 * source of truth, so the caller is handed an `onReconnect` hook to refetch,
 * and every handler must stay idempotent on chunk_index (PRD §24).
 */

import { getCurrentInstance, onBeforeUnmount, ref } from 'vue'

export type SSEStatus = 'idle' | 'connecting' | 'open' | 'closed'

interface Handlers {
  onTranscript?: (data: unknown) => void
  onChunkProcessing?: (data: unknown) => void
  onMeetingStatus?: (data: unknown) => void
  onError?: (data: unknown) => void
  /** Fired after the connection is re-established, never on first connect. */
  onReconnect?: () => void
}

export function useSSE() {
  const status = ref<SSEStatus>('idle')
  let source: EventSource | null = null
  let hasConnectedOnce = false

  function parse(raw: string): unknown {
    try {
      return JSON.parse(raw)
    } catch {
      return null
    }
  }

  function connect(url: string, handlers: Handlers) {
    disconnect()

    status.value = 'connecting'
    hasConnectedOnce = false

    const es = new EventSource(url)
    source = es

    es.onopen = () => {
      status.value = 'open'
      if (hasConnectedOnce) {
        // We were disconnected and missed events. Let the caller resync.
        handlers.onReconnect?.()
      }
      hasConnectedOnce = true
    }

    es.onerror = () => {
      // EventSource retries by itself unless it is CLOSED, so this is a
      // status update rather than a failure to act on.
      status.value = es.readyState === EventSource.CLOSED ? 'closed' : 'connecting'
    }

    const bind = (name: string, handler?: (data: unknown) => void) => {
      if (!handler) return
      es.addEventListener(name, (event) => {
        const data = parse((event as MessageEvent).data)
        if (data !== null) handler(data)
      })
    }

    bind('transcript', handlers.onTranscript)
    bind('chunk_processing', handlers.onChunkProcessing)
    bind('meeting_status', handlers.onMeetingStatus)
    bind('error', handlers.onError)
  }

  function disconnect() {
    source?.close()
    source = null
    status.value = 'closed'
  }

  // Only when a component owns this. A store-owned stream has to outlive the
  // views that come and go, or navigating away would cut the live transcript.
  if (getCurrentInstance()) {
    onBeforeUnmount(disconnect)
  }

  return { status, connect, disconnect }
}
