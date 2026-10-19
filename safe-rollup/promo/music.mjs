// Background music for the promo videos: an upbeat corporate cue, synthesized here so there is no
// license to clear. Major chords only, I, V, IV, V in C at about 122 BPM, build up to the end card,
// where the last V resolves to the tonic with a hit and rings out. The bar count and tempo are set
// per video so that moment falls exactly on the end card.
import { writeFileSync } from 'node:fs'

const SR = 44100
const TAU = Math.PI * 2
const freq = (note) => 440 * 2 ** ((note - 69) / 12)

const CHORDS = [
  [60, 64, 67], // C
  [59, 62, 67], // G
  [60, 65, 69], // F
  [59, 62, 67], // G
]
const BASS = [48, 43, 41, 43]
const ARP = [0, 1, 2, 3, 2, 1, 2, 3, 0, 1, 2, 3, 2, 3, 2, 1]
const BPM = 122

function random(seed) {
  let s = seed >>> 0
  return () => {
    s = (s * 1664525 + 1013904223) >>> 0
    return s / 4294967296
  }
}

const lowpass = (fc) => {
  const a = 1 - Math.exp((-TAU * fc) / SR)
  let y = 0
  return (x) => (y += a * (x - y))
}
const highpass = (fc) => {
  const lp = lowpass(fc)
  return (x) => x - lp(x)
}

// Karplus-Strong string: a burst of softened noise in a delay line that averages itself away.
function pluck(note, seconds, rand) {
  const len = Math.max(2, Math.round(SR / freq(note)))
  const line = new Float32Array(len)
  const soften = lowpass(5500)
  for (let i = 0; i < len; i++) line[i] = soften(rand() * 2 - 1)
  const out = new Float32Array(Math.round(seconds * SR))
  let at = 0
  for (let i = 0; i < out.length; i++) {
    const next = (at + 1) % len
    out[i] = line[at]
    line[at] = 0.4985 * (line[at] + line[next])
    at = next
  }
  return out
}

function pad(notes, seconds, attack, release) {
  const out = new Float32Array(Math.round(seconds * SR))
  const voices = notes.flatMap((n, i) => [-7, 7].map((cents, j) => ({ f: freq(n) * 2 ** (cents / 1200), phase: ((i * 2 + j) * 0.37) % 1 })))
  const lp1 = lowpass(2200)
  const lp2 = lowpass(2200)
  for (let i = 0; i < out.length; i++) {
    const t = i / SR
    let x = 0
    for (const v of voices) {
      v.phase += v.f / SR
      x += 2 * (v.phase - Math.floor(v.phase + 0.5))
    }
    const env = Math.min(1, t / attack) * Math.min(1, Math.max(0, (seconds - t) / release))
    out[i] = lp2(lp1(x / voices.length)) * env
  }
  return out
}

function kick() {
  const out = new Float32Array(Math.round(0.5 * SR))
  let phase = 0
  for (let i = 0; i < out.length; i++) {
    const t = i / SR
    phase += (TAU * (46 + 120 * Math.exp(-t / 0.03))) / SR
    out[i] = Math.sin(phase) * Math.exp(-t / 0.12)
  }
  return out
}

function noise(seconds, rand, shape) {
  const out = new Float32Array(Math.round(seconds * SR))
  for (let i = 0; i < out.length; i++) out[i] = shape(rand() * 2 - 1, i / SR)
  return out
}

const clap = (rand) => {
  const hp = highpass(900)
  const lp = lowpass(3500)
  return noise(0.3, rand, (x, t) => {
    const bursts = t < 0.03 ? 0.6 + 0.4 * Math.cos((TAU * t) / 0.01) : 1
    return lp(hp(x)) * bursts * Math.exp(-t / 0.09)
  })
}
const hat = (rand) => {
  const hp = highpass(7000)
  return noise(0.08, rand, (x, t) => hp(x) * Math.exp(-t / 0.022))
}
const crash = (rand) => {
  const hp = highpass(4500)
  return noise(3, rand, (x, t) => hp(x) * Math.exp(-t / 0.9))
}

// Freeverb: parallel damped combs into series allpasses, a little longer on the right.
function reverb(input, spread) {
  const combs = [1116, 1188, 1277, 1356, 1422, 1491, 1557, 1617].map((n) => ({ buf: new Float32Array(n + spread), at: 0, store: 0 }))
  const passes = [556, 441, 341, 225].map((n) => ({ buf: new Float32Array(n + spread), at: 0 }))
  const out = new Float32Array(input.length)
  for (let i = 0; i < input.length; i++) {
    const x = input[i] * 0.015
    let y = 0
    for (const c of combs) {
      const v = c.buf[c.at]
      c.store = v * 0.8 + c.store * 0.2
      c.buf[c.at] = x + c.store * 0.86
      c.at = (c.at + 1) % c.buf.length
      y += v
    }
    for (const p of passes) {
      const v = p.buf[p.at]
      p.buf[p.at] = y + v * 0.5
      p.at = (p.at + 1) % p.buf.length
      y = v - y
    }
    out[i] = y
  }
  return out
}

export function compose({ duration, end, seed = 7 }) {
  const n = Math.ceil(duration * SR)
  const rand = random(seed)
  const bars = Math.max(6, Math.round((end * BPM) / 240))
  const beat = end / (bars * 4)
  const bar = beat * 4
  // Bars beyond whole cycles go first, on the tonic, so the last bar before the end card is always V.
  const intro = bars % 4
  const tracks = { dry: [new Float32Array(n), new Float32Array(n)], send: [new Float32Array(n), new Float32Array(n)], ducked: [new Float32Array(n), new Float32Array(n)] }
  const duck = new Float32Array(n).fill(1)
  const put = (bus, at, data, gain, pan = 0) => {
    const start = Math.round(at * SR)
    const [l, r] = tracks[bus]
    const gl = gain * Math.min(1, 1 - pan)
    const gr = gain * Math.min(1, 1 + pan)
    for (let i = 0; i < data.length && start + i < n; i++) {
      if (start + i < 0) continue
      l[start + i] += data[i] * gl
      r[start + i] += data[i] * gr
    }
  }
  const kicked = (at) => {
    put('dry', at, kick(), 0.45)
    const start = Math.round(at * SR)
    for (let i = 0; i < 0.25 * SR && start + i < n; i++) duck[start + i] = Math.min(duck[start + i], 1 - 0.5 * Math.exp(-i / SR / 0.09))
  }

  for (let b = 0; b < bars; b++) {
    const t0 = b * bar
    const step = b < intro ? 0 : (b - intro) % 4
    const chord = CHORDS[step]
    const build = b === bars - 1
    put('ducked', t0, pad(chord, bar + 0.4, b === 0 ? 1.2 : 0.08, 0.4), 0.22)
    put('send', t0, pad(chord, bar + 0.4, 0.1, 0.4), 0.08)
    // Eighth notes until the groove starts, then sixteenths.
    const steps = b < 2 ? 8 : 16
    for (let s = 0; s < steps; s++) {
      const a = ARP[s % ARP.length]
      const note = (a === 3 ? chord[0] + 12 : chord[a]) + 12
      const p = pluck(note, 1.0, rand)
      const gain = (b === 0 ? 0.12 + 0.02 * s : steps === 16 ? 0.24 : 0.3) * (s % 2 === 0 ? 1 : 0.75)
      put('dry', t0 + (s * bar) / steps, p, gain, s % 2 === 0 ? -0.35 : 0.35)
      put('send', t0 + (s * bar) / steps, p, gain * 0.6)
    }
    if (b >= 1) {
      for (let q = 0; q < 4; q++) {
        if (!(build && q === 3)) kicked(t0 + q * beat)
        put('dry', t0 + q * beat + beat / 2, hat(rand), 0.2, 0.25)
        const bassNote = pad([BASS[step]], beat * 0.9, 0.005, 0.12)
        put('ducked', t0 + q * beat, bassNote, 0.24)
      }
    }
    if (b >= 2) {
      for (const q of [1, 3]) {
        if (build && q === 3) continue
        const c = clap(rand)
        put('dry', t0 + q * beat, c, 0.3)
        put('send', t0 + q * beat, c, 0.5)
      }
    }
    if (build) {
      for (let s = 8; s < 16; s++) {
        const c = clap(rand)
        put('dry', t0 + (s * beat) / 4, c, 0.06 + 0.025 * (s - 8))
      }
      const lp = lowpass(300)
      const riser = noise(bar, rand, (x, t) => {
        const k = t / bar
        return lp(x) * k * k * (1 + 6 * k)
      })
      put('send', t0, riser, 0.25)
      put('dry', t0, riser, 0.12)
    }
  }

  const tail = duration - end
  kicked(end)
  const c = crash(rand)
  put('dry', end, c, 0.22, -0.2)
  put('send', end, c, 0.3)
  put('ducked', end, pad([48, 55, 60, 64, 67, 72], tail + 0.5, 0.02, tail), 0.3)
  put('ducked', end, pad([36], tail + 0.5, 0.01, tail), 0.3)
  ;[64, 67, 72, 76, 79, 84].forEach((note, i) => {
    const p = pluck(note, tail, rand)
    put('dry', end + i * 0.03, p, 0.28, i % 2 ? 0.3 : -0.3)
    put('send', end + i * 0.03, p, 0.25)
  })

  const wet = [reverb(tracks.send[0], 0), reverb(tracks.send[1], 23)]
  const rumble = [0, 1].map(() => {
    const a = highpass(40)
    const b = highpass(40)
    return (x) => b(a(x))
  })
  const mix = [new Float32Array(n), new Float32Array(n)]
  for (let ch = 0; ch < 2; ch++) {
    for (let i = 0; i < n; i++) {
      mix[ch][i] = rumble[ch](tracks.dry[ch][i] + tracks.ducked[ch][i] * duck[i] + wet[ch][i] * 0.35)
    }
  }
  let sum = 0
  for (const ch of mix) for (const x of ch) sum += x * x
  const gain = 10 ** (-17 / 20) / Math.sqrt(sum / (2 * n))
  for (const ch of mix) {
    for (let i = 0; i < n; i++) {
      const t = i / SR
      const fade = Math.min(1, t / 0.15) * Math.min(1, (duration - t) / 1.2)
      ch[i] = Math.tanh(ch[i] * gain * 1.1) * fade * 0.95
    }
  }
  return mix
}

export function writeWav(path, [l, r]) {
  const data = Buffer.alloc(l.length * 4)
  for (let i = 0; i < l.length; i++) {
    data.writeInt16LE(Math.round(Math.max(-1, Math.min(1, l[i])) * 32767), i * 4)
    data.writeInt16LE(Math.round(Math.max(-1, Math.min(1, r[i])) * 32767), i * 4 + 2)
  }
  const head = Buffer.alloc(44)
  head.write('RIFF', 0)
  head.writeUInt32LE(36 + data.length, 4)
  head.write('WAVEfmt ', 8)
  head.writeUInt32LE(16, 16)
  head.writeUInt16LE(1, 20)
  head.writeUInt16LE(2, 22)
  head.writeUInt32LE(SR, 24)
  head.writeUInt32LE(SR * 4, 28)
  head.writeUInt16LE(4, 32)
  head.writeUInt16LE(16, 34)
  head.write('data', 36)
  head.writeUInt32LE(data.length, 40)
  writeFileSync(path, Buffer.concat([head, data]))
}
