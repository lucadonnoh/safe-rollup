// Preview server for the launch thread: each tweet from thread.md next to its video in out/, for
// watching from another device on the LAN. Byte ranges are supported because iOS Safari will not
// play a video without them. The page and the files are read on every request, so a re-render
// shows up on reload.
//
//   node serve.mjs            listens on 0.0.0.0:8765 (PORT to change it)
import { createReadStream, existsSync, readFileSync, statSync } from 'node:fs'
import { createServer } from 'node:http'
import { join } from 'node:path'

const here = new URL('.', import.meta.url).pathname
const out = join(here, 'out')
const PORT = Number(process.env.PORT ?? 8765)

const esc = (s) => s.replace(/[&<>"]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' })[c])

function page() {
  const thread = readFileSync(join(here, 'thread.md'), 'utf8')
  const tweets = [...thread.matchAll(/## (\d\/8) · `([^`]+)` · (\d+)\/280\n\n([\s\S]*?)(?=\n## |\s*$)/g)]
  const items = tweets
    .map(([, n, video, length, text]) => {
      const exists = existsSync(join(out, video))
      const v = exists ? `?v=${statSync(join(out, video)).mtimeMs}` : ''
      return `<article>
  <h2>${n} <span>${length}/280</span></h2>
  <p>${esc(text.trim()).replace(/\n/g, '<br>')}</p>
  ${exists ? `<video controls playsinline preload="metadata" src="/${esc(video)}${v}"></video><a href="/${esc(video)}" download>Download ${esc(video)}</a>` : `<em>${esc(video)} not rendered</em>`}
</article>`
    })
    .join('\n')
  return `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Safe Rollup launch thread</title>
<style>
  body { margin: 0; font: 16px/1.5 -apple-system, system-ui, sans-serif; color: #0a0b10; background: #f5f7fb; }
  main { max-width: 760px; margin: 0 auto; padding: 24px 16px 64px; }
  h1 { font-size: 22px; margin: 8px 0 24px; }
  article { background: #fff; border: 1px solid #e4e7ee; border-radius: 16px; padding: 18px; margin-bottom: 20px; }
  h2 { font-size: 15px; margin: 0 0 8px; color: #16209a; }
  h2 span { color: #5d6475; font-weight: 400; }
  p { margin: 0 0 14px; white-space: normal; }
  video { width: 100%; border-radius: 10px; background: #000; display: block; }
  a { display: inline-block; margin-top: 8px; font-size: 14px; color: #16209a; }
</style></head>
<body><main><h1>Safe Rollup launch thread</h1>
${items}
</main></body></html>`
}

function sendVideo(req, res, file) {
  const { size } = statSync(file)
  const head = { 'content-type': 'video/mp4', 'accept-ranges': 'bytes', 'cache-control': 'no-store' }
  const range = /^bytes=(\d*)-(\d*)$/.exec(req.headers.range ?? '')
  if (!range || (range[1] === '' && range[2] === '')) {
    res.writeHead(200, { ...head, 'content-length': size })
    return createReadStream(file).pipe(res)
  }
  const start = range[1] === '' ? Math.max(0, size - Number(range[2])) : Number(range[1])
  const end = range[1] !== '' && range[2] !== '' ? Math.min(Number(range[2]), size - 1) : size - 1
  if (start > end || start >= size) {
    res.writeHead(416, { 'content-range': `bytes */${size}` })
    return res.end()
  }
  res.writeHead(206, { ...head, 'content-length': end - start + 1, 'content-range': `bytes ${start}-${end}/${size}` })
  createReadStream(file, { start, end }).pipe(res)
}

createServer((req, res) => {
  const path = decodeURIComponent(new URL(req.url ?? '/', 'http://localhost').pathname)
  if (path === '/') {
    res.writeHead(200, { 'content-type': 'text/html; charset=utf-8', 'cache-control': 'no-store' })
    return res.end(page())
  }
  const name = path.slice(1)
  if (/^[\w.-]+\.mp4$/.test(name) && existsSync(join(out, name))) return sendVideo(req, res, join(out, name))
  res.writeHead(404, { 'content-type': 'text/plain' })
  res.end('not found')
}).listen(PORT, '0.0.0.0', () => console.log(`http://0.0.0.0:${PORT}`))
