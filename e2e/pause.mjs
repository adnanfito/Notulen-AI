// Pause/resume in the real UI, against the real backend.
//
// The thing worth proving is that a pause does not just freeze the display:
// the timer must stop, the already-recorded audio must survive, and the
// meeting's final duration must reflect recorded audio rather than the clock.

import { chromium } from 'playwright'
import { mkdirSync } from 'node:fs'

const BASE = 'http://localhost:5273'
const API = 'http://localhost:8180/api/v1'
const OUT = process.argv[2] ?? 'shots'
mkdirSync(OUT, { recursive: true })

const browser = await chromium.launch({
  args: [
    '--use-fake-ui-for-media-stream',
    '--use-fake-device-for-media-stream',
    '--autoplay-policy=no-user-gesture-required',
  ],
})

const page = await browser.newPage({ viewport: { width: 1280, height: 900 } })
const errors = []
page.on('console', (m) => m.type() === 'error' && errors.push(m.text()))
page.on('pageerror', (e) => errors.push(`pageerror: ${e.message}`))

const timer = () => page.locator('.font-mono').first().innerText()
let failures = 0
function check(label, ok, detail = '') {
  console.log(`${ok ? 'ok  ' : 'FAIL'}  ${label}${detail ? '  ' + detail : ''}`)
  if (!ok) failures++
}

await page.goto(`${BASE}/meetings/new`, { waitUntil: 'domcontentloaded' })
// Let the view mount before driving it; clicking into a half-hydrated page
// silently does nothing.
await page.waitForSelector('input[name="audio-source"]')
await page.getByLabel(/judul/i).fill('Pause: uji otomatis')
await page.getByRole('button', { name: /mulai merekam/i }).click()
await page.waitForSelector('.font-mono', { timeout: 20000 })

const meetingId = await fetch(`${API}/meetings?limit=1`)
  .then((r) => r.json())
  .then((d) => d.items[0].id)

await page.waitForTimeout(6000)
const beforePause = await timer()

await page.getByRole('button', { name: /^jeda$/i }).click()
await page.waitForTimeout(500)
await page.screenshot({ path: `${OUT}/01-paused.png`, fullPage: true })

check('shows Dijeda', (await page.locator('main').innerText()).includes('Dijeda'))
check('button becomes Lanjutkan', await page.getByRole('button', { name: /lanjutkan/i }).isVisible())

const atPause = await timer()
await page.waitForTimeout(8000)
const afterWait = await timer()
check('timer frozen while paused', atPause === afterWait, `${atPause} -> ${afterWait}`)

// The chunk flushed by the pause should already be uploading.
const chunksWhilePaused = await fetch(`${API}/meetings/${meetingId}`)
  .then((r) => r.json())
  .then((d) => d.transcript_chunks.length)
check('pause flushed a chunk', chunksWhilePaused >= 1, `${chunksWhilePaused} chunk(s)`)

await page.getByRole('button', { name: /lanjutkan/i }).click()
await page.waitForTimeout(4000)
await page.screenshot({ path: `${OUT}/02-resumed.png`, fullPage: true })

const afterResume = await timer()
check('timer resumes', afterResume !== afterWait, `${afterWait} -> ${afterResume}`)
check('no time added for the pause', toSeconds(afterResume) < toSeconds(beforePause) + 14,
  `${beforePause} + ~4s recorded -> ${afterResume}`)

await page.getByRole('button', { name: /hentikan rekaman/i }).click()
await page.waitForURL((u) => /\/meetings\/[0-9a-f-]{36}$/.test(u.pathname), { timeout: 120000 })
await page.waitForTimeout(3000)

const detail = await fetch(`${API}/meetings/${meetingId}`).then((r) => r.json())
// ~6s + ~4s recorded across an 8s pause; the clock said ~18s.
check('stored duration excludes the pause', detail.duration_seconds < 16,
  `${detail.duration_seconds}s stored`)
check('all chunks transcribed', detail.transcript_chunks.every((c) => c.status !== 'FAILED'),
  `${detail.transcript_chunks.length} chunk(s)`)

console.log('console errors:', errors.length ? errors.join(' ;; ') : 'none')
console.log(failures === 0 ? 'PASS' : `FAIL (${failures})`)
console.log('meeting:', meetingId)

function toSeconds(mmss) {
  const [m, s] = mmss.split(':').map(Number)
  return m * 60 + s
}

await browser.close()
process.exit(failures === 0 ? 0 : 1)
