// Timeline for the promo videos. render.mjs calls seek(t) once per frame, so every frame is exact;
// opened in a browser, a page plays in real time instead (?t=5 starts five seconds in).
//
// Markup:
//   data-at="1.2"            enter at 1.2s, with data-anim (up, fade, pop, ...; default up) over data-dur seconds
//   data-out="6"             leave at 6s, with data-out-anim (default out) over data-out-dur seconds
//   data-kids-at="3" data-kids-step="0.2"   stagger the children's entrances (data-kids-anim)
//   data-count="9555"        count from data-from (0) starting at data-count-at over data-count-dur,
//                            printed with data-decimals, data-prefix and data-suffix
//   data-type                type the text out from data-type-at at data-cps characters per second
// <body data-duration="17" data-end="14"> adds the shared end card at 14s; data-brand-at shows the brand bar.
(() => {
  const ticks = []
  const ease = (x) => 1 - (1 - x) ** 3
  const clamp = (x) => Math.max(0, Math.min(1, x))
  const mark =
    '<svg viewBox="0 0 26 26" aria-hidden="true"><rect x="1" y="1" width="24" height="24" rx="7" fill="#16209a"/>' +
    '<circle cx="8" cy="13" r="2.4" fill="#fff"/><circle cx="13" cy="13" r="2.4" fill="#fff"/>' +
    '<circle cx="18" cy="13" r="2.4" fill="#fff" opacity="0.35"/></svg>'
  const logo = `<div class="logo">${mark}<span class="wm">Safe <i>Rollup</i></span></div>`
  const progress = (t, at, dur) => ease(clamp((t - at) / dur))
  // Dots that travel along SVG paths: runs is [{ path: id, at: seconds, dur, color }], drawn into group.
  const travel = (group, runs) => {
    const dots = runs.map(({ path, at, dur = 0.8, color = '#16209a', r = 6 }) => {
      const c = document.createElementNS('http://www.w3.org/2000/svg', 'circle')
      c.setAttribute('r', String(r))
      c.setAttribute('fill', color)
      group.append(c)
      return { c, path: document.getElementById(path), at, dur }
    })
    ticks.push((t) => {
      for (const { c, path, at, dur } of dots) {
        const p = (t - at) / dur
        c.style.opacity = p < 0 || p > 1 ? '0' : '1'
        const pt = path.getPointAtLength(ease(clamp(p)) * path.getTotalLength())
        c.setAttribute('cx', String(pt.x))
        c.setAttribute('cy', String(pt.y))
      }
    })
  }
  window.promo = { ticks, ease, clamp, mark, logo, progress, travel }

  function chrome(body) {
    if (body.dataset.brandAt !== 'none') {
      // Shown from the first frame unless it comes in later, so the preview frame carries the brand.
      const at = Number(body.dataset.brandAt ?? 0)
      const timing = at > 0 ? ` data-at="${at}" data-anim="fade"` : ''
      body.insertAdjacentHTML(
        'beforeend',
        `<div class="brandbar"${timing}>${logo}</div><div class="urlbar"${timing}>saferollup.fyi</div>`,
      )
    }
    if (body.dataset.end) {
      const at = Number(body.dataset.end)
      body.insertAdjacentHTML(
        'beforeend',
        `<section class="scene endcard bg-hero" data-at="${at}" data-anim="fade" data-dur="0.6">
          <div data-at="${at + 0.15}" data-anim="pop">${logo}</div>
          <div class="site" data-at="${at + 0.4}">saferollup.fyi</div>
          <div class="sub" data-at="${at + 0.6}"><span class="pill live">${body.dataset.endPill ?? 'Live on Sepolia'}</span></div>
        </section>`,
      )
    }
  }

  function schedule() {
    for (const parent of document.querySelectorAll('[data-kids-at]')) {
      const at = Number(parent.dataset.kidsAt)
      const step = Number(parent.dataset.kidsStep ?? 0.15)
      ;[...parent.children].forEach((kid, i) => {
        kid.dataset.at ??= String(at + i * step)
        if (parent.dataset.kidsAnim) kid.dataset.anim ??= parent.dataset.kidsAnim
        if (parent.dataset.kidsDur) kid.dataset.dur ??= parent.dataset.kidsDur
      })
    }
    for (const el of document.querySelectorAll('[data-at], [data-out]')) {
      const list = []
      if (el.dataset.at) list.push(`${el.dataset.anim ?? 'up'} ${el.dataset.dur ?? 0.8}s var(--ease) ${el.dataset.at}s both`)
      // An exit must not fill backwards, or it would show the element before its entrance.
      if (el.dataset.out) list.push(`${el.dataset.outAnim ?? 'out'} ${el.dataset.outDur ?? 0.5}s ease-in ${el.dataset.out}s forwards`)
      el.style.animation = list.join(', ')
    }
    for (const el of document.querySelectorAll('[data-count]')) {
      const to = Number(el.dataset.count)
      const from = Number(el.dataset.from ?? 0)
      const at = Number(el.dataset.countAt ?? el.dataset.at ?? 0)
      const dur = Number(el.dataset.countDur ?? 1.6)
      const decimals = Number(el.dataset.decimals ?? 0)
      const fmt = (v) =>
        (el.dataset.prefix ?? '') +
        v.toLocaleString('en-US', { minimumFractionDigits: decimals, maximumFractionDigits: decimals }) +
        (el.dataset.suffix ?? '')
      ticks.push((t) => {
        el.textContent = fmt(from + (to - from) * progress(t, at, dur))
      })
    }
    for (const el of document.querySelectorAll('[data-type]')) {
      const text = el.textContent
      const at = Number(el.dataset.typeAt ?? el.dataset.at ?? 0)
      const cps = Number(el.dataset.cps ?? 30)
      ticks.push((t) => {
        el.textContent = text.slice(0, Math.max(0, Math.floor((t - at) * cps)))
      })
    }
  }

  window.seek = (t) => {
    for (const a of document.getAnimations()) {
      a.pause()
      a.currentTime = t * 1000
    }
    for (const f of ticks) f(t)
  }

  window.ready = (async () => {
    if (document.readyState === 'loading') await new Promise((r) => addEventListener('DOMContentLoaded', r))
    const body = document.body
    window.DURATION = Number(body.dataset.duration)
    if (window.prepare) await window.prepare()
    chrome(body)
    schedule()
    await document.fonts.ready
    window.seek(0)
  })()

  if (!window.RENDER) {
    window.ready.then(() => {
      const start = performance.now() - Number(new URLSearchParams(location.search).get('t') ?? 0) * 1000
      const loop = () => {
        window.seek(((performance.now() - start) / 1000) % window.DURATION)
        requestAnimationFrame(loop)
      }
      requestAnimationFrame(loop)
    })
  }
})()
