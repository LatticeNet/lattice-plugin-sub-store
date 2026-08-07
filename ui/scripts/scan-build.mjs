import { readdir, readFile } from 'node:fs/promises'
import path from 'node:path'

const root = process.argv[2]

if (!root) {
  console.error('usage: node ./scripts/scan-build.mjs <dist-dir>')
  process.exit(2)
}

const distRoot = path.resolve(root)
const indexPath = path.join(distRoot, 'index.html')
const html = await readFile(indexPath, 'utf8')

if (/<script(?![^>]*\bsrc=)[^>]*>/i.test(html)) {
  throw new Error('inline script detected in built index.html')
}
if (/<style[^>]*>/i.test(html)) {
  throw new Error('inline style detected in built index.html')
}

const allowlistedUrls = new Set([
  'http://www.w3.org/2000/svg',
  'http://www.w3.org/1999/xlink',
  'http://www.w3.org/1998/Math/MathML'
])
const allowlistedPrefixes = ['https://vuejs.org/error-reference/']

/**
 * The dev harness must never reach the bundle.
 *
 * It mounts the real screens against a FAKE host with canned records. Shipping
 * it would put a second, lying data source inside a signed artifact — and the
 * one thing a reviewer of a signed bundle should not have to do is work out
 * which of two hosts a screen is talking to.
 *
 * Vite builds from index.html alone, so this cannot happen today. It is
 * asserted because "cannot happen today" is a property of the build config,
 * and build configs change.
 */
const DEV_MARKERS = ['fakeHost', 'DevApp', 'dev harness', 'canned records']
for (const filePath of await listFiles(distRoot)) {
  const contents = await readFile(filePath, 'utf8')
  const found = DEV_MARKERS.find((marker) => contents.includes(marker))
  if (found) {
    throw new Error(
      `dev harness leaked into ${path.relative(distRoot, filePath)}: ${found}`
    )
  }
}

for (const filePath of await listFiles(distRoot)) {
  const contents = await readFile(filePath, 'utf8')
  const matches = contents.match(/https?:\/\/[^"')\s]+/g) ?? []
  const disallowed = matches.filter(
    (value) =>
      !allowlistedUrls.has(value) &&
      !allowlistedPrefixes.some((prefix) => value.startsWith(prefix))
  )
  if (disallowed.length > 0) {
    throw new Error(`external URL detected in ${path.relative(distRoot, filePath)}: ${disallowed[0]}`)
  }
}

async function listFiles(dir) {
  const entries = await readdir(dir, { withFileTypes: true })
  const files = []
  for (const entry of entries) {
    const fullPath = path.join(dir, entry.name)
    if (entry.isDirectory()) {
      files.push(...(await listFiles(fullPath)))
      continue
    }
    if (entry.isFile()) {
      files.push(fullPath)
    }
  }
  return files
}
