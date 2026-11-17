// How much audio does the stop/restart cycle actually lose?
//
// Each chunk must be a standalone WebM file, which means a fresh recorder per
// chunk. This measures the dead time between one recorder stopping and the
// next starting — audio that is genuinely not captured.

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

const gaps = await page.evaluate(async () => {
  const stream = await navigator.mediaDevices.getUserMedia({ audio: true })
  const measured = []
  let running = true
  let stoppedAt = 0

  function cycle() {
    if (!running) return
    if (stoppedAt) measured.push(performance.now() - stoppedAt)

    const rec = new MediaRecorder(stream, { mimeType: 'audio/webm;codecs=opus' })
    rec.onstop = () => {
      stoppedAt = performance.now()
      cycle()
    }
    rec.start()
    setTimeout(() => rec.state === 'recording' && rec.stop(), 700)
  }

  cycle()
  await new Promise((r) => setTimeout(r, 6000))
  running = false
  stream.getTracks().forEach((t) => t.stop())

  return measured
})

console.log('restart gaps (ms):', gaps.map((g) => g.toFixed(1)).join(', '))
if (gaps.length) {
  const max = Math.max(...gaps)
  const avg = gaps.reduce((a, b) => a + b, 0) / gaps.length
  console.log(`avg ${avg.toFixed(1)} ms, max ${max.toFixed(1)} ms`)
  console.log(`=> over a 90 s chunk that is ${((max / 90_000) * 100).toFixed(4)}% of the audio`)
}

await browser.close()
