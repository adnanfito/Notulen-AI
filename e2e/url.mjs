// The address bar should carry the meeting id as soon as recording starts,
// without disturbing the recording or the back button.

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

let failures = 0
const check = (label, ok, detail = '') => {
  console.log(`${ok ? 'ok  ' : 'FAIL'}  ${label}${detail ? '  ' + detail : ''}`)
  if (!ok) failures++
}

const UUID = /\/meetings\/[0-9a-f-]{36}$/

// Start from the dashboard, so there is a previous entry to go back to.
await page.goto(BASE, { waitUntil: 'domcontentloaded' })
// The "new meeting" link is on the dashboard whether or not it has rows.
await page.getByRole('link', { name: /rapat baru/i }).waitFor({ timeout: 20000 })

await page.getByRole('link', { name: /rapat baru/i }).click()
await page.waitForSelector('input[name="audio-source"]')
check('setup page is /meetings/new', page.url().endsWith('/meetings/new'), page.url())

await page.getByLabel(/judul/i).fill('URL: uji otomatis')
await page.getByRole('button', { name: /mulai merekam/i }).click()
await page.waitForSelector('.font-mono', { timeout: 20000 })
await page.waitForTimeout(1200)

const url = page.url()
check('url carries the meeting id', UUID.test(new URL(url).pathname), url)
await page.screenshot({ path: `${OUT}/01-recording.png`, fullPage: true })

const meetingId = url.split('/').pop()
const listed = await fetch(`${API}/meetings?limit=1`).then((r) => r.json())
check('url id matches the meeting', listed.items[0].id === meetingId)

// The URL swap must not have disturbed the recording itself.
const body = await page.locator('main').innerText()
check('still recording', body.includes('Merekam'))
check('heading shows the typed title',
  (await page.locator('h2').first().innerText()).includes('URL: uji otomatis'))
check('pause button present', await page.getByRole('button', { name: /^jeda$/i }).isVisible())

await page.waitForTimeout(2500)
const timerMoved = (await page.locator('.font-mono').first().innerText()) !== '00:00'
check('timer running', timerMoved)

// Leaving must still prompt — the earlier guard has to survive this change.
page.once('dialog', (d) => d.dismiss())
await page.getByRole('link', { name: /notulen ai/i }).click()
await page.waitForTimeout(1000)
check('leave guard still asks', UUID.test(new URL(page.url()).pathname), page.url())

// And leaving for real still finishes the meeting.
page.once('dialog', (d) => d.accept())
await page.getByRole('link', { name: /notulen ai/i }).click()

let status = 'RECORDING'
for (let i = 0; i < 60 && status === 'RECORDING'; i++) {
  await page.waitForTimeout(2000)
  status = await fetch(`${API}/meetings/${meetingId}`).then((r) => r.json()).then((d) => d.status)
}
check('meeting finished on leave', status !== 'RECORDING', status)

// Back lands on /meetings/new — that is genuinely where the user was, and
// with no active recording the view renders the setup form. What matters is
// that it is a usable page, not a stale recording.
await page.goBack()
await page.waitForTimeout(1500)
const backBody = await page.locator('main').innerText()
check('back shows the setup form', backBody.includes('Sumber audio'))
check('back shows no stale recording', !backBody.includes('Hentikan rekaman'))

console.log('console errors:', errors.length ? errors.join(' ;; ') : 'none')
console.log(failures === 0 ? 'PASS' : `FAIL (${failures})`)
console.log('meeting:', meetingId)

await browser.close()
process.exit(failures === 0 ? 0 : 1)
