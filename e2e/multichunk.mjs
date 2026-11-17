// Regression test for the chunk-boundary bug.
//
// MediaRecorder's timeslice put the WebM header in the first blob only, so
// chunk 0 transcribed and every chunk after it failed as undecodable. This
// records long enough to cross several boundaries and asserts that EVERY
// chunk comes back COMPLETED.
//
// Run the stack with a short chunk length first:
//   VITE_AUDIO_CHUNK_SECONDS=8 docker compose up -d --build frontend

import { chromium } from 'playwright'

const BASE = 'http://localhost:5273'
const RECORD_MS = Number(process.env.RECORD_MS ?? 30_000)

const browser = await chromium.launch({
  args: [
    '--use-fake-ui-for-media-stream',
    '--use-fake-device-for-media-stream',
    '--autoplay-policy=no-user-gesture-required',
  ],
})

const page = await browser.newPage({ viewport: { width: 1280, height: 900 } })
await page.goto(BASE, { waitUntil: 'networkidle' })

await page.getByRole('link', { name: /rapat baru/i }).click()
await page.waitForURL('**/meetings/new')
await page.getByLabel(/judul/i).fill('Uji Multi-Chunk')
await page.getByRole('button', { name: /mulai merekam/i }).click()
await page.waitForSelector('text=/\\d\\d:\\d\\d/', { timeout: 15000 })

console.log(`recording for ${RECORD_MS / 1000}s…`)
await page.waitForTimeout(RECORD_MS)

const sent = await page.locator('dl div').nth(0).innerText()
console.log('counters while recording:', sent.replace(/\n/g, '='))

await page.getByRole('button', { name: /hentikan rekaman/i }).click()
await page.waitForURL((url) => /\/meetings\/[0-9a-f-]{36}$/.test(url.pathname), { timeout: 120_000 })

const meetingId = page.url().split('/').pop()
await page.waitForTimeout(3000)

// Read the truth from the API, not the rendered page.
const detail = await page.evaluate(
  async (id) => (await fetch(`/api/v1/meetings/${id}`)).json(),
  meetingId,
)

console.log('\nmeeting :', detail.title, '|', detail.status)
console.log('chunks  :', detail.transcript_chunks.length)

let failed = 0
for (const c of detail.transcript_chunks) {
  if (c.status === 'FAILED') failed++
  const preview = (c.transcript_text || '(empty)').slice(0, 50)
  console.log(
    `  #${c.chunk_index} ${c.status.padEnd(10)} ${String(c.start_time_sec).padStart(3)}-${String(c.end_time_sec).padEnd(3)}s  ${preview}`,
  )
}

console.log(`\n=> ${failed} of ${detail.transcript_chunks.length} chunks FAILED`)
console.log(failed === 0 ? 'PASS — every chunk decoded' : 'FAIL — chunk boundary still broken')

await browser.close()
process.exit(failed === 0 ? 0 : 1)
