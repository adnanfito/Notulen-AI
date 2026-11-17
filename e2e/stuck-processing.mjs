// A meeting left at PROCESSING by a backend restart must not be a dead end.
//
// Summarization runs in a goroutine. A restart abandons it, but the row stays
// at PROCESSING — and the retry endpoint used to read that status as proof
// that work was under way, refusing with 409 forever. The transcript was
// intact the whole time and there was no way to reach it.
//
//   node stuck-processing.mjs <meeting-id-with-a-transcript>

const API = 'http://localhost:8180/api/v1'

const source = process.argv[2]
if (!source) {
  console.error('usage: node stuck-processing.mjs <meeting-id>')
  process.exit(1)
}

let failures = 0
const check = (label, ok, detail = '') => {
  console.log(`${ok ? 'ok  ' : 'FAIL'}  ${label}${detail ? '  ' + detail : ''}`)
  if (!ok) failures++
}

const get = (id) => fetch(`${API}/meetings/${id}`).then((r) => r.json())

const original = await get(source)
if (!original.transcript_chunks?.length) {
  console.error('that meeting has no transcript to work with')
  process.exit(1)
}

// Build a throwaway copy by combining the source with itself is not possible,
// so work on the source directly but restore its status afterwards.
const restoreStatus = original.status
console.log(`using ${original.title} (${original.transcript_chunks.length} chunks)`)

// Simulate the orphan: mark it PROCESSING with nothing running.
// There is no API for this by design, so drive it through psql.
const { execSync } = await import('node:child_process')
const sql = (q) =>
  execSync(
    `docker compose exec -T postgres psql -U postgres -d meeting_notes -t -A -c "${q}"`,
    { cwd: '..', encoding: 'utf8', stdio: ['ignore', 'pipe', 'ignore'] },
  ).trim()

sql(`UPDATE meetings SET status='PROCESSING' WHERE id='${source}'`)
check('meeting is now stuck at PROCESSING', (await get(source)).status === 'PROCESSING')

// The retry must be accepted, not refused with "already being generated".
const res = await fetch(`${API}/meetings/${source}/summarize`, { method: 'POST' })
check('retry is accepted, not 409', res.status === 202, `HTTP ${res.status}`)

if (res.status === 202) {
  let status = 'PROCESSING'
  for (let i = 0; i < 90 && status === 'PROCESSING'; i++) {
    await new Promise((r) => setTimeout(r, 2000))
    status = (await get(source)).status
  }
  check('summarizes through to the end', status === 'COMPLETED', status)

  const after = await get(source)
  check('transcript survived', (after.raw_transcript ?? '').length > 0,
    `${(after.raw_transcript ?? '').length} chars`)
  check('notes present', after.summary_data !== null)
} else {
  sql(`UPDATE meetings SET status='${restoreStatus}' WHERE id='${source}'`)
}

console.log(failures === 0 ? 'PASS' : `FAIL (${failures})`)
process.exit(failures === 0 ? 0 : 1)
