// Renders promo/videos/*.html to 1920x1080 H.264 MP4s in promo/out, one exact frame at a time.
//
//   node render.mjs                 every video
//   node render.mjs 03 05           only the videos whose file names start with 03 or 05
//   node render.mjs 02 --stills 4,9 PNG stills at 4s and 9s instead of a video
//   node render.mjs --check         report slides whose text cannot be read before they leave
//   node render.mjs --music         put freshly composed music on the rendered videos, without re-rendering
//   --refresh                       fetch live data and screenshots again (cached in .cache)
//
// Needs ffmpeg with libx264 and a Chromium: CHROME=/path/to/chrome, or Playwright's own install.
import { spawn } from 'node:child_process'
import { existsSync, mkdirSync, readFileSync, readdirSync, renameSync, rmSync, writeFileSync } from 'node:fs'
import { extname, join } from 'node:path'
import { chromium } from 'playwright-core'
import { compose, writeWav } from './music.mjs'

const here = new URL('.', import.meta.url).pathname
const root = join(here, '..')
const cache = join(here, '.cache')
const out = join(here, 'out')
const FPS = 30
const SITE = 'https://saferollup.fyi'
const EXPLORER = 'https://explorer.saferollup.fyi'

const args = process.argv.slice(2)
const flag = (name) => args.includes(name)
const stillsAt = args.includes('--stills') ? args[args.indexOf('--stills') + 1].split(',').map(Number) : undefined
// Reading speed for on-screen text: about subtitle speed, plus a moment to take in each slide.
const WORDS_PER_SECOND = 3.4
const SETTLE = 0.6
const MAX_SECONDS = 20
const picks = args.filter((a, i) => !a.startsWith('--') && args[i - 1] !== '--stills')
const videos = readdirSync(join(here, 'videos'))
  .filter((f) => f.endsWith('.html') && (picks.length === 0 || picks.some((p) => f.startsWith(p))))
  .sort()

mkdirSync(cache, { recursive: true })
mkdirSync(join(out, 'stills'), { recursive: true })
const browser = await chromium.launch({ executablePath: process.env.CHROME, headless: true })

async function snapshot() {
  if (!flag('--refresh') && existsSync(join(cache, 'status.json'))) return
  const status = await (await fetch(`${SITE}/api/status`)).json()
  writeFileSync(join(cache, 'status.json'), JSON.stringify(status))
  const page = await browser.newPage({ viewport: { width: 1440, height: 900 }, deviceScaleFactor: 2, reducedMotion: 'reduce' })
  await page.goto(`${SITE}/#network`, { waitUntil: 'networkidle' })
  await page.waitForFunction(() => document.querySelector('#forced li:not(.muted)'), null, { timeout: 60_000 })
  await page.locator('#network').scrollIntoViewIfNeeded()
  await page.screenshot({ path: join(cache, 'site-network.png') })
  await page.goto(EXPLORER, { waitUntil: 'networkidle' })
  await page.waitForTimeout(4000)
  await page.screenshot({ path: join(cache, 'explorer.png') })
  await page.close()
}

const types = { '.html': 'text/html', '.js': 'text/javascript', '.css': 'text/css', '.json': 'application/json', '.png': 'image/png', '.svg': 'image/svg+xml' }

async function open(file) {
  const page = await browser.newPage({ viewport: { width: 1920, height: 1080 }, deviceScaleFactor: 1 })
  await page.addInitScript(() => {
    window.RENDER = true
  })
  await page.route('https://promo.test/**', (route) => {
    const path = decodeURIComponent(new URL(route.request().url()).pathname)
    const file = path.startsWith('/cache/') ? join(cache, path.slice('/cache/'.length)) : join(root, path)
    if (!existsSync(file)) return route.fulfill({ status: 404, body: '' })
    return route.fulfill({ body: readFileSync(file), contentType: types[extname(file)] ?? 'application/octet-stream' })
  })
  const errors = []
  page.on('pageerror', (e) => errors.push(e.message))
  await page.goto(`https://promo.test/promo/videos/${file}`)
  await page.evaluate(() => window.ready)
  if (errors.length > 0) throw new Error('page error', { cause: { file, errors } })
  return page
}

// Every text block must stay up long enough to read, and so must each slide as a whole. Footnotes,
// diagram labels and .glance elements (visual texture, like block numbers) are exempt.
async function check(file) {
  const page = await open(file)
  const problems = await page.evaluate(({ rate, settle, max }) => {
    window.seek(window.DURATION)
    const end = Number(document.body.dataset.end ?? window.DURATION)
    const inherited = (el, key, pick, start) => {
      let t = start
      for (let e = el; e && e !== document.body; e = e.parentElement) if (e.dataset?.[key] !== undefined) t = pick(t, Number(e.dataset[key]))
      return t
    }
    const words = (el) => el.textContent.trim().split(/\s+/).filter(Boolean).length
    const out = []
    if (window.DURATION > max) out.push(`${window.DURATION}s long, over ${max}s`)
    for (const scene of document.querySelectorAll('.scene:not(.endcard)')) {
      const exit = Math.min(inherited(scene, 'out', Math.min, Infinity), end)
      const blocks = [...scene.querySelectorAll('[data-at], [data-type], [data-count]')].filter(
        (el) => !el.querySelector('[data-at]') && !el.closest('.foot, .glance, svg') && words(el) > 0,
      )
      if (blocks.length === 0) continue
      const at = (el) => inherited(el, 'at', Math.max, 0)
      const first = Math.min(...blocks.map(at))
      const total = blocks.reduce((n, el) => n + words(el), 0)
      const need = total / rate + settle
      const label = scene.querySelector('h1, h2')?.textContent.trim().slice(0, 40) ?? scene.className
      if (exit - first < need) out.push(`"${label}": ${total} words need ${need.toFixed(1)}s, shown ${(exit - first).toFixed(1)}s`)
      for (const el of blocks) {
        const own = words(el) / rate + 0.4
        if (exit - at(el) < own) out.push(`"${label}" > "${el.textContent.trim().slice(0, 40)}": needs ${own.toFixed(1)}s, shown ${(exit - at(el)).toFixed(1)}s`)
      }
    }
    return out
  }, { rate: WORDS_PER_SECOND, settle: SETTLE, max: MAX_SECONDS })
  await page.close()
  console.log(`${file}: ${problems.length === 0 ? 'readable' : ''}`)
  for (const p of problems) console.log(`  ${p}`)
  return problems.length
}

function ffmpeg(args) {
  const p = spawn('ffmpeg', ['-y', '-loglevel', 'error', ...args], { stdio: ['pipe', 'inherit', 'inherit'] })
  const done = new Promise((resolve, reject) => p.on('close', (code) => (code === 0 ? resolve() : reject(new Error('ffmpeg failed', { cause: { code, args } })))))
  return { stdin: p.stdin, done }
}

// The music lands on the end card, so it is composed for each video's timing.
async function withMusic(video, target, name, duration, end) {
  const wav = join(cache, `${name}.wav`)
  writeWav(wav, compose({ duration, end }))
  const tmp = `${target}.tmp.mp4`
  await ffmpeg(['-i', video, '-i', wav, '-map', '0:v', '-map', '1:a', '-c:v', 'copy', '-c:a', 'aac', '-b:a', '192k', '-shortest', '-movflags', '+faststart', tmp]).done
  renameSync(tmp, target)
}

async function render(file) {
  const page = await open(file)
  const name = file.replace(/\.html$/, '')
  if (stillsAt) {
    for (const t of stillsAt) {
      await page.evaluate((t) => window.seek(t), t)
      await page.screenshot({ path: join(out, 'stills', `${name}-${t}s.png`) })
    }
    await page.close()
    return
  }
  const { duration, end } = await page.evaluate(() => ({ duration: window.DURATION, end: Number(document.body.dataset.end) }))
  const target = join(out, `${name}.mp4`)
  const silent = join(cache, `${name}.silent.mp4`)
  const encoder = ffmpeg([
    '-f', 'image2pipe', '-framerate', String(FPS), '-i', '-',
    '-c:v', 'libx264', '-preset', 'slow', '-crf', '16', '-pix_fmt', 'yuv420p', '-profile:v', 'high', silent,
  ])
  const frames = Math.round(duration * FPS)
  const started = Date.now()
  for (let i = 0; i < frames; i++) {
    await page.evaluate((t) => window.seek(t), i / FPS)
    const png = await page.screenshot()
    if (!encoder.stdin.write(png)) await new Promise((r) => encoder.stdin.once('drain', r))
  }
  encoder.stdin.end()
  await encoder.done
  await page.close()
  await withMusic(silent, target, name, duration, end)
  rmSync(silent)
  console.log(`${target} ${duration}s, ${frames} frames in ${Math.round((Date.now() - started) / 1000)}s`)
}

if (flag('--music')) {
  for (const file of videos) {
    const name = file.replace(/\.html$/, '')
    const html = readFileSync(join(here, 'videos', file), 'utf8')
    const attr = (key) => Number(new RegExp(`data-${key}="([\\d.]+)"`).exec(html)[1])
    await withMusic(join(out, `${name}.mp4`), join(out, `${name}.mp4`), name, attr('duration'), attr('end'))
    console.log(`${name}.mp4: music added`)
  }
  await browser.close()
  process.exit(0)
}

await snapshot()
if (flag('--check')) {
  let problems = 0
  for (const file of videos) problems += await check(file)
  process.exitCode = problems > 0 ? 1 : 0
} else {
  for (const file of videos) await render(file)
}
await browser.close()
