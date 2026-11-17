// Exercises the manual "regenerate notes" button in the real UI.
//
//   node resummarize.mjs <meeting-id> [outDir]
//
// Creates nothing and deletes nothing — point it at a meeting that has a
// transcript but no notes.

import { chromium } from 'playwright'
import { mkdirSync } from 'node:fs'

const id = process.argv[2]
if (!id) {
  console.error('usage: node resummarize.mjs <meeting-id> [outDir]')
  process.exit(1)
}
const OUT = process.argv[3] ?? 'shots'
mkdirSync(OUT, { recursive: true })

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1280, height: 900 } })

const errors = []
page.on('console', (m) => m.type() === 'error' && errors.push(m.text()))
page.on('pageerror', (e) => errors.push(`pageerror: ${e.message}`))

await page.goto(`http://localhost:5273/meetings/${id}`, { waitUntil: 'networkidle' })
await page.waitForSelector('article')

const button = page.getByRole('button', { name: /buat notulen sekarang/i })
console.log('empty-state button visible:', await button.isVisible().catch(() => false))
await page.screenshot({ path: `${OUT}/01-before.png`, fullPage: true })

await button.click()
console.log('clicked')

// PROCESSING banner proves the request landed and the page reacted.
const processing = await page
  .waitForSelector('text=/Menyusun notulen/i', { timeout: 15000 })
  .then(() => true)
  .catch(() => false)
console.log('shows PROCESSING:', processing)
await page.screenshot({ path: `${OUT}/02-processing.png`, fullPage: true })

// The page must update itself over SSE, with no reload.
const done = await page
  .waitForSelector('text=/Ringkasan/', { timeout: 180000 })
  .then(() => true)
  .catch(() => false)

console.log('notes appeared without reload:', done)
await page.waitForTimeout(1000)
await page.screenshot({ path: `${OUT}/03-after.png`, fullPage: true })

if (done) {
  const body = await page.locator('article').innerText()
  console.log('page now shows:', body.slice(0, 220).replace(/\n+/g, ' | '))
}

console.log('console errors:', errors.length ? errors.join(' ;; ') : 'none')

await browser.close()
process.exit(done ? 0 : 1)
