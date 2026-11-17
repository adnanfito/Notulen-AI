// Diagnostic: are MediaRecorder timeslice chunks independently decodable?
//
// EBML files start with the magic 0x1A45DFA3. If only the first blob carries
// it, every later chunk is a headerless cluster fragment and no decoder can
// read it on its own.

import { chromium } from 'playwright'

const browser = await chromium.launch({
  args: [
    '--use-fake-ui-for-media-stream',
    '--use-fake-device-for-media-stream',
    '--autoplay-policy=no-user-gesture-required',
  ],
})

const page = await browser.newPage()
await page.goto('http://localhost:5273')

const probe = await page.evaluate(async () => {
  const stream = await navigator.mediaDevices.getUserMedia({ audio: true })
  const blobs = []
  const gaps = []
  let recording = true
  let lastStop = 0

  // Mirrors useAudioRecorder: one recorder per chunk, restarted on stop.
  function cycle() {
    if (!recording) return
    if (lastStop) gaps.push(Date.now() - lastStop)

    const rec = new MediaRecorder(stream, { mimeType: 'audio/webm;codecs=opus' })
    rec.ondataavailable = (e) => e.data.size && blobs.push(e.data)
    rec.onstop = () => {
      lastStop = Date.now()
      if (recording) cycle()
    }
    rec.start()
    setTimeout(() => rec.state === 'recording' && rec.stop(), 1000)
  }

  cycle()
  await new Promise((r) => setTimeout(r, 3500))
  recording = false
  stream.getTracks().forEach((t) => t.stop())
  await new Promise((r) => setTimeout(r, 300))
  window.__gaps = gaps

  const out = []
  for (const [i, blob] of blobs.entries()) {
    const head = new Uint8Array(await blob.slice(0, 4).arrayBuffer())
    const hex = [...head].map((b) => b.toString(16).padStart(2, '0')).join(' ')
    out.push({
      index: i,
      size: blob.size,
      firstBytes: hex,
      hasEbmlHeader: hex.startsWith('1a 45 df a3'),
    })
  }
  return { chunks: out, gaps: window.__gaps ?? [] }
})

const { chunks: result, gaps } = probe
console.log('blobs produced:', result.length)
for (const r of result) {
  console.log(
    `  chunk ${r.index}: ${String(r.size).padStart(6)} bytes  first=[${r.firstBytes}]  EBML header: ${r.hasEbmlHeader ? 'YES' : 'NO'}`,
  )
}

const withHeader = result.filter((r) => r.hasEbmlHeader).length
console.log(`\n=> ${withHeader}/${result.length} chunks are standalone WebM files`)

await browser.close()
