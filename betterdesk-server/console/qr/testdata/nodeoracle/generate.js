'use strict'

// Writes the test vectors for the Go package console/qr with the npm
// package qrcode that the Node console uses. See README.md next to this
// file. Run it in the dev container from /repo/web-nodejs:
//
//   node ../betterdesk-server/console/qr/testdata/nodeoracle/generate.js
//
// The texts come from a seeded generator, so a second run writes the same
// files.

const fs = require('fs')
const path = require('path')
const zlib = require('zlib')
const { createRequire } = require('module')

// The script lives outside web-nodejs, so the packages are looked up from
// the working directory.
const load = createRequire(path.join(process.cwd(), 'package.json'))
const QRCode = load('qrcode')
const Version = load('qrcode/lib/core/version')
const Mode = load('qrcode/lib/core/mode')
const ECLevel = load('qrcode/lib/core/error-correction-level')

const outDir = path.join(__dirname, '..')
const LEVELS = ['L', 'M', 'Q', 'H']

// ---- seeded random numbers (mulberry32) ----

let seed = 0x5152434f
function random () {
  seed = (seed + 0x6D2B79F5) | 0
  let t = seed
  t = Math.imul(t ^ (t >>> 15), t | 1)
  t ^= t + Math.imul(t ^ (t >>> 7), t | 61)
  return ((t ^ (t >>> 14)) >>> 0) / 4294967296
}
function int (min, max) { return min + Math.floor(random() * (max - min + 1)) }
function pick (list) { return list[int(0, list.length - 1)] }
function chars (alphabet, n) {
  const set = Array.from(alphabet)
  let s = ''
  for (let i = 0; i < n; i++) s += pick(set)
  return s
}
// A short random word repeated up to n characters: as varied as the long
// texts need to be, and small in the compressed vector file.
function repeated (alphabet, n) {
  const word = chars(alphabet, int(5, 13))
  return word.repeat(Math.ceil(n / word.length)).slice(0, n)
}
function bytes (n) {
  const b = Buffer.alloc(n)
  for (let i = 0; i < n; i++) b[i] = int(0, 255)
  return b
}
// A length that is usually small and sometimes large.
function skewed (max) { return 1 + Math.floor(Math.pow(random(), 3) * max) }

const DIGITS = '0123456789'
const UPPER = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ'
const ALNUM_ONLY = UPPER + ' $%*+-./:' // the alphanumeric set without digits
const ALNUM = DIGITS + ALNUM_ONLY
const LOWER = 'abcdefghijklmnopqrstuvwxyz'
const BYTE_ASCII = LOWER + '_=&?#@!~,;()[]{}<>"\'\\^`|\t\n\r'
const BASE32 = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'
const TWO_BYTE = 'äöüßéèñçøåÄÖÜλπΩжЖяЯ¢£§±×÷'
const THREE_BYTE = '€✓→中文日本語한국어ไทย“”…'
const FOUR_BYTE = '😀🚀🔑🧪𝄞𐍈🇩🇪'
// Characters the npm package would write in Kanji mode if a converter were
// registered: hiragana, katakana, CJK, full width forms, Greek, Cyrillic
// and a few symbols.
const KANJI_LIKE = 'あいうえおかきくけこアイウエオカキクケコ漢字東京日本語一二三' +
  'ＡＢＣ１２３！？αβγδΩЖдя§¨±´×÷※←↑→↓★☆‐―‘’“”‥…∥≠　、。〃々〆'

// ---- the list of texts ----

const cases = []
let turn = 0
// Adds a text. Without a level the four levels take turns.
function add (text, level) {
  cases.push({ text, level: level || LEVELS[turn++ % 4] })
}
function addBytes (buffer, level) {
  cases.push({ raw: buffer, level: level || LEVELS[turn++ % 4] })
}

// Empty text: the package refuses it.
for (const level of LEVELS) add('', level)

// Every ASCII character alone, and every pair of classes.
for (let c = 0; c < 128; c++) add(String.fromCharCode(c), 'M')
for (const a of ['7', 'K', 'x', 'é']) {
  for (const b of ['3', 'Z', '%', 'q', '€', '😀']) add(a + b)
}

// Each mode around the capacity of each version at each level: one
// character less than fits, exactly what fits, one more.
const alphabets = [
  [Mode.NUMERIC, DIGITS],
  [Mode.ALPHANUMERIC, ALNUM_ONLY],
  [Mode.BYTE, LOWER]
]
for (const level of LEVELS) {
  for (let version = 1; version <= 40; version++) {
    for (const [mode, alphabet] of alphabets) {
      const capacity = Version.getCapacity(version, ECLevel.from(level), mode)
      for (const n of [capacity - 1, capacity, capacity + 1]) {
        add(repeated(alphabet, n), level)
      }
    }
  }
}
// Far too long.
add(repeated(DIGITS, 10000), 'L')
add(repeated(LOWER, 3000), 'H')

// Digits only.
for (let i = 0; i < 300; i++) add(chars(DIGITS, skewed(i % 10 === 0 ? 600 : 80)))

// The alphanumeric set, digits included.
for (let i = 0; i < 400; i++) add(chars(ALNUM, skewed(i % 10 === 0 ? 500 : 80)))
for (let i = 0; i < 100; i++) add(chars(ALNUM_ONLY, skewed(60)))

// otpauth addresses as the console builds them for two-factor setup.
const NAMES = ['alice', 'bob', 'admin', 'Operator7', 'j.doe@example.com', 'müller',
  'Zoë Faßbender', 'user+tag@firma.de', '田中', 'SUPPORT-01', 'x', 'a_very_long_account_name_for_a_technician_2026']
const ISSUERS = ['BetterDesk', 'BetterDesk Console', 'Rath & Blume', 'ACME-IT', 'bd']
function otpauth () {
  const issuer = pick(ISSUERS)
  const account = random() < 0.3 ? chars(LOWER + DIGITS + '.-_@', int(1, 40)) : pick(NAMES)
  const label = `${encodeURIComponent(issuer)}:${encodeURIComponent(account)}`
  const params = new URLSearchParams({ secret: chars(BASE32, 32), issuer, digits: '6', period: '30' })
  return `otpauth://totp/${label}?${params.toString()}`
}
add('otpauth://totp/BetterDesk:alice?secret=JBSWY3DPEHPK3PXP&issuer=BetterDesk', 'M')
for (let i = 0; i < 500; i++) add(otpauth(), i % 2 ? 'M' : undefined)

// Web addresses, lower and upper case.
function url () {
  const host = `${chars(LOWER, int(3, 12))}.${pick(['example.com', 'rathblume.de', 'local', 'co.uk'])}`
  const port = random() < 0.4 ? `:${int(80, 65535)}` : ''
  const pathPart = random() < 0.7 ? `/${chars(LOWER + DIGITS + '-_/', int(1, 40))}` : ''
  const query = random() < 0.5 ? `?id=${int(1, 999999999)}&t=${chars(LOWER + UPPER + DIGITS, int(4, 30))}` : ''
  return `${pick(['http', 'https', 'rustdesk'])}://${host}${port}${pathPart}${query}`
}
for (let i = 0; i < 300; i++) {
  const u = url()
  add(i % 5 === 0 ? u.toUpperCase() : u)
}

// Keys: base64 of 32 bytes (the server's public key), hex, base32.
for (let i = 0; i < 300; i++) add(bytes(32).toString('base64'), i % 2 ? 'M' : undefined)
for (let i = 0; i < 60; i++) add(bytes(int(8, 64)).toString('hex'))
for (let i = 0; i < 60; i++) add(bytes(int(8, 64)).toString('hex').toUpperCase())
for (let i = 0; i < 60; i++) add(chars(BASE32, int(16, 64)))

// Client configuration addresses as keyService.js builds them: the JSON
// {host, relay, api, key} in base64 without padding, reversed.
function configUri (extra) {
  const host = `${chars(LOWER, int(2, 20))}.${chars(LOWER, int(3, 14))}.${pick(['de', 'com', 'net', 'internal'])}`
  const payload = {
    host,
    relay: random() < 0.6 ? host : `relay.${host}:${int(1024, 65535)}`,
    api: `${pick(['http', 'https'])}://${host}:${int(80, 65535)}`,
    key: bytes(32).toString('base64')
  }
  if (extra) payload.note = chars(LOWER + UPPER + DIGITS + ' .,-äß', extra)
  const b64 = Buffer.from(JSON.stringify(payload)).toString('base64').replace(/=+$/, '')
  return `rustdesk://config/${b64.split('').reverse().join('')}`
}
for (let i = 0; i < 300; i++) add(configUri(0), i % 2 ? 'M' : undefined)
// Long ones, 300 to 1,500 characters.
for (let i = 0; i < 120; i++) add(configUri(int(60, 960)), i % 2 ? 'M' : undefined)

// UTF-8 with two, three and four byte characters, alone and mixed with ASCII.
for (let i = 0; i < 400; i++) {
  const sets = [TWO_BYTE, THREE_BYTE, FOUR_BYTE, LOWER, DIGITS, UPPER]
  let s = ''
  const parts = int(1, 8)
  for (let p = 0; p < parts; p++) s += chars(i % 4 === 0 ? pick(sets.slice(0, 3)) : pick(sets), int(1, 12))
  add(s)
}

// Characters that would be Kanji mode with a converter; here they are bytes.
for (let i = 0; i < 120; i++) add(chars(KANJI_LIKE, skewed(60)))
for (let i = 0; i < 80; i++) {
  let s = ''
  for (let p = int(1, 6); p > 0; p--) s += chars(pick([KANJI_LIKE, DIGITS, UPPER, LOWER]), int(1, 10))
  add(s)
}

// Runs of the three classes in every order and many lengths. This is what
// the package's search for the shortest segmentation has to decide on.
const CLASSES = [DIGITS, ALNUM_ONLY, BYTE_ASCII + TWO_BYTE]
function runs (total, longest) {
  let s = ''
  let last = -1
  while (Array.from(s).length < total) {
    let c = int(0, 2)
    if (c === last) c = (c + 1) % 3
    last = c
    s += chars(CLASSES[c], skewed(longest))
  }
  return s
}
for (let i = 0; i < 1500; i++) add(runs(int(1, 250), pick([2, 4, 8, 16, 40])))
for (let i = 0; i < 80; i++) add(runs(int(300, 1500), pick([2, 4, 8, 16, 40])), i % 2 ? 'M' : undefined)
for (let i = 0; i < 10; i++) add(runs(int(1500, 2600), pick([3, 8, 30])), 'L')

// Two classes taking turns with fixed run lengths, repeated.
const PAIRS = [[DIGITS, ALNUM_ONLY], [DIGITS, LOWER], [ALNUM_ONLY, LOWER]]
for (const [first, second] of PAIRS) {
  for (let a = 1; a <= 8; a++) {
    for (let b = 1; b <= 8; b++) {
      for (const repeat of [2, 5, 20]) {
        let s = ''
        for (let r = 0; r < repeat; r++) s += chars(first, a) + chars(second, b)
        add(s)
      }
    }
  }
}

// Plain bytes that are not text: any byte except the 45 alphanumeric
// characters, so the whole input is one byte segment. Most of these are not
// valid UTF-8 and cannot be a JavaScript string; the package gets them as
// an explicit byte segment.
const NOT_ALNUM = []
for (let b = 0; b < 256; b++) if (!ALNUM.includes(String.fromCharCode(b))) NOT_ALNUM.push(b)
for (let i = 0; i < 200; i++) {
  const b = Buffer.alloc(skewed(i % 10 === 0 ? 1200 : 120))
  for (let k = 0; k < b.length; k++) b[k] = pick(NOT_ALNUM)
  addBytes(b)
}
addBytes(Buffer.from(NOT_ALNUM), 'M')

// ---- run the package ----

function packBits (modules) {
  const out = Buffer.alloc(Math.ceil(modules.length / 8))
  for (let i = 0; i < modules.length; i++) {
    if (modules[i]) out[i >> 3] |= 0x80 >> (i & 7)
  }
  return out.toString('base64')
}

let lines = ''
let failed = 0
const versionsSeen = new Set()
for (const c of cases) {
  const line = { l: c.level }
  let input
  if (c.raw) {
    line.x = c.raw.toString('base64')
    input = [{ data: new Uint8Array(c.raw), mode: 'byte' }]
  } else {
    line.t = c.text
    input = c.text
  }
  try {
    const qr = QRCode.create(input, { errorCorrectionLevel: c.level })
    line.v = qr.version
    line.m = qr.maskPattern
    line.b = packBits(qr.modules.data)
    versionsSeen.add(c.level + qr.version)
  } catch (e) {
    line.e = 1
    failed++
  }
  lines += JSON.stringify(line) + '\n'
}
fs.writeFileSync(path.join(outDir, 'vectors.jsonl.gz'), zlib.gzipSync(lines, { level: 9 }))

// ---- images ----

// The call the console makes, for a text of every version and for a set of
// widths and margins: under the 21 pixel minimum, smaller than the code,
// not a multiple of its size, and large.
const pngCases = []
for (let version = 1; version <= 40; version++) {
  const text = chars(LOWER + DIGITS, Version.getCapacity(version, ECLevel.M, Mode.BYTE))
  // Margin 2 is what the console asks for; the other margins for a spread
  // of versions.
  const margins = version <= 12 || version % 10 === 0 ? [0, 2, 4] : [2]
  for (const margin of margins) pngCases.push({ text, level: 'M', width: 256, margin })
}
const samples = [
  ['otpauth://totp/BetterDesk:alice?secret=JBSWY3DPEHPK3PXP&issuer=BetterDesk', 'M'],
  [bytes(32).toString('base64'), 'M'],
  [configUri(0), 'M'],
  ['HELLO WORLD', 'Q'],
  [configUri(400), 'L'],
  [chars(DIGITS, 12), 'H']
]
for (const [text, level] of samples) {
  for (const width of [0, 20, 21, 33, 64, 100, 177, 200, 250, 257, 300, 333, 500, 777, 1024]) {
    for (const margin of [0, 2, 4]) pngCases.push({ text, level, width, margin })
  }
}

async function writeImages () {
  let out = ''
  for (const c of pngCases) {
    const options = {
      errorCorrectionLevel: c.level,
      type: 'image/png',
      margin: c.margin,
      color: { dark: '#000000', light: '#ffffff' }
    }
    if (c.width) options.width = c.width
    const dataUrl = await QRCode.toDataURL(c.text, options)
    const prefix = 'data:image/png;base64,'
    if (!dataUrl.startsWith(prefix)) throw new Error('unexpected data URL')
    out += JSON.stringify({ t: c.text, l: c.level, w: c.width, g: c.margin, p: dataUrl.slice(prefix.length) }) + '\n'
  }
  fs.writeFileSync(path.join(outDir, 'images.jsonl.gz'), zlib.gzipSync(out, { level: 9 }))
}

writeImages().then(() => {
  console.log(`${cases.length} texts (${failed} refused), ${versionsSeen.size} of 160 version and level pairs, ${pngCases.length} images`)
}).catch((e) => {
  console.error(e)
  process.exit(1)
})
