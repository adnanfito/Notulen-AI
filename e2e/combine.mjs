// Combining several meetings into one set of notes, driven through the UI.
//
//   node combine.mjs "<title substring>" [outDir]
//
// Picks every meeting whose title matches, combines them, and checks the
// result — including that the sources come through untouched.

import { chromium } from 'playwright'
import { mkdirSync } from 'node:fs'

const match = process.argv[2]
if (!match) {
  console.error('usage: node combine.mjs "<title substring>" [outDir]')
  process.exit(1)
}
const OUT = process.argv[3] ?? 'shots'
mkdirSync(OUT, { recursive: true })

const BASE = 'http://localhost:5273'
const API = 'http://localhost:8180/api/v1'

const before = await fetch(`${API}/meetings?limit=50`).then((r) => r.json())
const sources = before.items.filter((m) => m.title.includes(match) && !m.is_combined)

if (sources.length < 2) {
  console.error(`need at least 2 meetings matching "${match}", found ${sources.length}`)
  process.exit(1)
}
console.log(`combining ${sources.length} meetings matching "${match}"`)

// Remember the sources exactly as they are, to prove combining leaves them be.
const snapshots = await Promise.all(
  sources.map(async (m) => {
    const d = await fetch(`${API}/meetings/${m.id}`).then((r) => r.json())
    return {
      id: d.id,
      title: d.title,
      status: d.status,
      chunks: d.transcript_chunks.length,
      transcript: (d.raw_transcript ?? '').length,
      duration: d.duration_seconds,
    }
  }),
)

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1280, height: 900 } })
const errors = []
page.on('console', (m) => m.type() === 'error' && errors.push(m.text()))
page.on('pageerror', (e) => errors.push(`pageerror: ${e.message}`))

let failures = 0
const check = (label, ok, detail = '') => {
  console.log(`${ok ? 'ok  ' : 'FAIL'}  ${label}${detail ? '  ' + detail : ''}`)
  if (!ok) failures++
}

await page.goto(BASE, { waitUntil: 'domcontentloaded' })
await page.waitForSelector('ul li')

await page.getByRole('button', { name: /^gabungkan$/i }).first().click()
await page.waitForTimeout(400)
check('selection mode opens', await page.locator('text=/Pilih diskusi yang mau digabungkan/').isVisible())

// A split discussion keeps its title, so labels are not unique — tick every
// enabled checkbox belonging to a matching row.
const rows = page.locator('ul li').filter({ hasText: match })
const rowCount = await rows.count()
for (let i = 0; i < rowCount; i++) {
  const box = rows.nth(i).locator('input[type=checkbox]')
  if (await box.isEnabled()) await box.check()
}
console.log(`ticked ${rowCount} rows`)
await page.waitForTimeout(300)
await page.screenshot({ path: `${OUT}/01-selected.png`, fullPage: true })

page.once('dialog', (d) => {
  console.log('confirm says:', d.message().split('\n')[0])
  d.accept()
})
await page.getByRole('button', { name: /^gabungkan$/i }).last().click()

await page.waitForURL((u) => /\/meetings\/[0-9a-f-]{36}$/.test(u.pathname), { timeout: 60000 })
const combinedId = page.url().split('/').pop()
console.log('combined meeting:', combinedId)

const ok = await page
  .waitForSelector('text=/Ringkasan/', { timeout: 300000 })
  .then(() => true)
  .catch(() => false)
check('notes generated', ok)
await page.waitForTimeout(1500)
await page.screenshot({ path: `${OUT}/02-combined.png`, fullPage: true })

const combined = await fetch(`${API}/meetings/${combinedId}`).then((r) => r.json())

check('marked as combined', (combined.source_meeting_ids ?? []).length === snapshots.length,
  `${(combined.source_meeting_ids ?? []).length} sources`)
check('shows provenance', (await page.locator('article').innerText()).includes('Gabungan dari'))

const expectedChunks = snapshots.reduce((n, s) => n + s.chunks, 0)
check('chunks copied across', combined.transcript_chunks.length === expectedChunks,
  `${combined.transcript_chunks.length} of ${expectedChunks}`)

const expectedDuration = snapshots.reduce((n, s) => n + s.duration, 0)
check('duration is the sum', combined.duration_seconds === expectedDuration,
  `${combined.duration_seconds}s vs ${expectedDuration}s`)

// Timestamps must run forward across the join, or the transcript is unusable.
let previousEnd = -1
let monotonic = true
for (const c of combined.transcript_chunks) {
  if (c.start_time_sec < previousEnd) monotonic = false
  previousEnd = c.end_time_sec
}
check('timestamps run continuously', monotonic, `ends at ${previousEnd}s`)

// The whole promise of this feature: the originals are left alone.
for (const s of snapshots) {
  const after = await fetch(`${API}/meetings/${s.id}`).then((r) => r.json())
  const same =
    after.status === s.status &&
    after.transcript_chunks.length === s.chunks &&
    (after.raw_transcript ?? '').length === s.transcript
  check(`source untouched: ${s.title.slice(0, 28)}`, same)
}

console.log('console errors:', errors.length ? errors.join(' ;; ') : 'none')
console.log(failures === 0 ? 'PASS' : `FAIL (${failures})`)

await browser.close()
process.exit(failures === 0 ? 0 : 1)
