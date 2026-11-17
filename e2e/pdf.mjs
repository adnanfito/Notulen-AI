// Renders the meeting page through the print pipeline and saves the PDF, so
// the exported document can actually be looked at rather than assumed correct.
//
//   node pdf.mjs <meeting-id> [outDir]

import { chromium } from 'playwright'
import { mkdirSync } from 'node:fs'

const id = process.argv[2]
if (!id) {
  console.error('usage: node pdf.mjs <meeting-id> [outDir]')
  process.exit(1)
}
const OUT = process.argv[3] ?? 'shots'
mkdirSync(OUT, { recursive: true })

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1280, height: 900 } })

await page.goto(`http://localhost:5273/meetings/${id}`, { waitUntil: 'networkidle' })

// Fail loudly rather than silently exporting a PDF of an error banner.
await page.waitForSelector('article', { timeout: 20000 }).catch(async () => {
  const body = await page.locator('main').innerText()
  console.error('page did not render the meeting:', body.slice(0, 200))
  await browser.close()
  process.exit(1)
})
await page.waitForTimeout(500)

// Collapse a topic first: the print stylesheet must reveal it anyway, since
// v-show keeps it in the DOM.
const toggles = page.locator('.print-as-heading')
if (await toggles.count()) {
  await toggles.first().click()
  await page.waitForTimeout(200)
}

async function render(label, withTranscript) {
  const box = page.locator('input[type=checkbox]').last()
  const isTranscriptToggle = (await page.locator('label:has-text("transkrip") input').count()) > 0
  if (isTranscriptToggle) {
    const t = page.locator('label:has-text("transkrip") input')
    if ((await t.isChecked()) !== withTranscript) await t.click()
  }
  void box

  await page.emulateMedia({ media: 'print' })
  const path = `${OUT}/notulen-${label}.pdf`
  await page.pdf({ path, format: 'A4', printBackground: false })

  // A PNG of the same print media, so the layout can be eyeballed.
  await page.screenshot({ path: `${OUT}/print-${label}.png`, fullPage: true })
  await page.emulateMedia({ media: 'screen' })

  console.log(`wrote ${path}`)
}

await render('notes', false)
await render('with-transcript', true)

await browser.close()
