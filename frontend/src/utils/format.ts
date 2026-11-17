export function formatDuration(totalSeconds: number): string {
  const seconds = Math.max(0, Math.floor(totalSeconds))
  const h = Math.floor(seconds / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  const s = seconds % 60
  const pad = (n: number) => String(n).padStart(2, '0')
  return h > 0 ? `${pad(h)}:${pad(m)}:${pad(s)}` : `${pad(m)}:${pad(s)}`
}

/** Compact duration for list rows: "45 dtk", "58 mnt", "1 j 12 mnt". */
export function formatDurationShort(totalSeconds: number): string {
  const seconds = Math.max(0, Math.round(totalSeconds))
  // Rounding a short meeting to minutes renders it as "0 mnt", which reads
  // like the recording failed.
  if (seconds < 60) return `${seconds} dtk`

  const minutes = Math.round(seconds / 60)
  if (minutes < 60) return `${minutes} mnt`
  const h = Math.floor(minutes / 60)
  const m = minutes % 60
  return m === 0 ? `${h} j` : `${h} j ${m} mnt`
}

export function formatDate(iso: string): string {
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return '-'
  return new Intl.DateTimeFormat('id-ID', {
    day: 'numeric',
    month: 'short',
    year: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  }).format(date)
}

export const STATUS_LABELS: Record<string, string> = {
  RECORDING: 'Merekam',
  PROCESSING: 'Memproses',
  COMPLETED: 'Selesai',
  FAILED: 'Gagal',
}
