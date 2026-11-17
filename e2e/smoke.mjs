// End-to-end smoke test: drives the real app in a real browser.
//
// Run against a running stack:  node e2e/smoke.mjs
// Needs Playwright, which is deliberately NOT a project dependency (it
// would bloat the dev container for a script run by hand):
//   npm i -g playwright && npx playwright install chromium
//
// Chromium's fake media device supplies audio, so recording is exercised
// for real — MediaRecorder, chunk upload, SSE, stop, and the notes page.

import { chromium } from 'playwright'
import { mkdirSync } from 'node:fs'

const BASE = 'http://localhost:5273'
const OUT = process.argv[2] ?? 'e2e/shots'
mkdirSync(OUT, { recursive: true })

const browser = await chromium.launch({
  args: [
    // Grant mic + feed a synthetic tone, so recording can be driven headlessly.
    '--use-fake-ui-for-media-stream',
    '--use-fake-device-for-media-stream',
    '--autoplay-policy=no-user-gesture-required',
  ],
})

const context = await browser.newContext({
  permissions: ['microphone'],
  viewport: { width: 1280, height: 900 },
})
const page = await context.newPage()

const consoleErrors = []
page.on('console', (msg) => {
  if (msg.type() === 'error') consoleErrors.push(msg.text())
})
page.on('pageerror', (err) => consoleErrors.push(`pageerror: ${err.message}`))

function log(...args) {
  console.log(...args)
}

// --- Dashboard ---
await page.goto(BASE, { waitUntil: 'networkidle' })
await page.screenshot({ path: `${OUT}/01-dashboard.png`, fullPage: true })
log('DASHBOARD heading:', await page.locator('h2').first().innerText())
log('DASHBOARD rows   :', await page.locator('ul li').count())
log('DASHBOARD search :', (await page.locator('input[type=search]').count()) > 0)

// --- New meeting page ---
await page.getByRole('link', { name: /rapat baru/i }).click()
await page.waitForURL('**/meetings/new')
await page.waitForTimeout(400)
await page.screenshot({ path: `${OUT}/02-new-meeting.png`, fullPage: true })

const sources = await page.locator('input[name="audio-source"]').count()
log('NEW radio sources:', sources)
log('NEW start button :', await page.getByRole('button', { name: /mulai merekam/i }).isVisible())

// --- Actually record ---
await page.getByLabel(/judul/i).fill('Rapat Uji Otomatis')
await page.getByRole('button', { name: /mulai merekam/i }).click()

// The timer appearing is proof MediaRecorder started.
await page.waitForSelector('text=/\\d\\d:\\d\\d/', { timeout: 15000 })
log('RECORDING started')

await page.waitForTimeout(2500)
await page.screenshot({ path: `${OUT}/03-recording.png`, fullPage: true })
log('RECORDING timer  :', await page.locator('.font-mono').first().innerText())
log('RECORDING conn   :', await page.locator('dl div').nth(3).innerText())

// Stop early: the point is to prove the whole pipeline runs, not to wait out
// a full 90-second chunk.
await page.getByRole('button', { name: /hentikan rekaman/i }).click()

try {
  await page.waitForURL((url) => /\/meetings\/[0-9a-f-]{36}$/.test(url.pathname), {
    timeout: 60000,
  })
  log('NAVIGATED to detail:', page.url())
  await page.waitForTimeout(2500)
  await page.screenshot({ path: `${OUT}/04-detail.png`, fullPage: true })
  log('DETAIL heading   :', await page.locator('h2').first().innerText())
  log('DETAIL body      :', (await page.locator('article').innerText()).slice(0, 300).replace(/\n+/g, ' | '))
} catch (err) {
  log('NAV FAILED:', err.message)
  await page.screenshot({ path: `${OUT}/04-stuck.png`, fullPage: true })
  log('STUCK ON:', page.url())
}

log('CONSOLE ERRORS:', consoleErrors.length ? consoleErrors.join(' ;; ') : 'none')

await browser.close()
