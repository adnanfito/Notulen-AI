// The recording must survive navigation.
//
// It used to live in the recording view, so leaving the page killed it — hence
// the confirmation dialog. It now lives in the store, so the user can browse
// the rest of the app and come back to a still-running recording.

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

// Any dialog at all is now a failure: there should be nothing to confirm.
let dialogs = 0
page.on('dialog', (d) => {
  dialogs++
  console.log('  unexpected dialog:', d.message().split('\n')[0])
  void d.dismiss()
})

let failures = 0
const check = (label, ok, detail = '') => {
  console.log(`${ok ? 'ok  ' : 'FAIL'}  ${label}${detail ? '  ' + detail : ''}`)
  if (!ok) failures++
}
const timer = () => page.locator('.font-mono').first().innerText()
const seconds = (mmss) => {
  const [m, s] = mmss.split(':').map(Number)
  return m * 60 + s
}

await page.goto(BASE, { waitUntil: 'domcontentloaded' })
await page.getByRole('link', { name: /rapat baru/i }).waitFor({ timeout: 20000 })
await page.getByRole('link', { name: /rapat baru/i }).click()
await page.waitForSelector('input[name="audio-source"]')

await page.getByLabel(/judul/i).fill('Navigasi: uji otomatis')
await page.getByRole('button', { name: /mulai merekam/i }).click()
await page.waitForSelector('.font-mono', { timeout: 20000 })

// The timer appears as soon as recording starts; the URL is rewritten a tick
// later, so wait for it rather than racing it.
await page.waitForURL((u) => /\/meetings\/[0-9a-f-]{36}$/.test(u.pathname), { timeout: 15000 })
const meetingId = page.url().split('/').pop()
check('url carries the meeting id', /^[0-9a-f-]{36}$/.test(meetingId ?? ''), page.url())

await page.waitForTimeout(4000)
const beforeLeaving = await timer()

// --- leave for the dashboard -------------------------------------------
await page.getByRole('link', { name: /notulen ai/i }).click()
await page.waitForTimeout(1500)

check('left without any confirmation', dialogs === 0)
check('landed on the dashboard', new URL(page.url()).pathname === '/', page.url())
await page.screenshot({ path: `${OUT}/01-browsing.png`, fullPage: true })

const stillRecording = await fetch(`${API}/meetings/${meetingId}`)
  .then((r) => r.json())
  .then((d) => d.status)
check('meeting still RECORDING', stillRecording === 'RECORDING', stillRecording)

const indicator = page.locator('header a[href*="/meetings/"]')
check('header shows a way back', await indicator.isVisible())

// --- wander somewhere else, then come back -----------------------------
await page.waitForTimeout(5000)
await indicator.click()
await page.waitForTimeout(1200)

check('back on the meeting url', page.url().endsWith(meetingId), page.url())

const body = await page.locator('main').innerText()
check('shows the live recorder, not the finished page', body.includes('Hentikan rekaman'))
check('no "belum diselesaikan" banner', !body.includes('belum diselesaikan'))
check('keeps the typed title', body.includes('Navigasi: uji otomatis'))

const afterReturn = await timer()
check('timer kept running while away', seconds(afterReturn) > seconds(beforeLeaving),
  `${beforeLeaving} -> ${afterReturn}`)
await page.screenshot({ path: `${OUT}/02-returned.png`, fullPage: true })

// --- stop from here ----------------------------------------------------
await page.getByRole('button', { name: /hentikan rekaman/i }).click()

let status = 'RECORDING'
for (let i = 0; i < 90 && status === 'RECORDING'; i++) {
  await page.waitForTimeout(2000)
  status = await fetch(`${API}/meetings/${meetingId}`).then((r) => r.json()).then((d) => d.status)
}
check('stops normally', status !== 'RECORDING', status)

await page.waitForTimeout(2000)
check('indicator gone once stopped', !(await indicator.isVisible().catch(() => false)))

console.log('console errors:', errors.length ? errors.join(' ;; ') : 'none')
console.log(failures === 0 ? 'PASS' : `FAIL (${failures})`)
console.log('meeting:', meetingId)

await browser.close()
process.exit(failures === 0 ? 0 : 1)
