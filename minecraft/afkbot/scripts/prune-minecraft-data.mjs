// minecraft-data ships every version of both editions — 430 MB, against the
// ~15 MB one Bedrock protocol version actually resolves to. Left intact it is
// three quarters of the runtime image.
//
// The keep-set is derived from the package's own dataPaths.json rather than
// listed here, because a version inherits most of its files from older
// directories and does so unpredictably: 1.26.40 reaches back to bedrock
// 1.16.201 for windows and across editions to pc/1.17 for effects and
// materials. A hand-written list would look right and fail at runtime.
import { readFileSync, readdirSync, rmSync, statSync } from 'node:fs'
import { join } from 'node:path'

const version = process.argv[2]
if (!version) {
  console.error('usage: prune-minecraft-data.mjs <bedrock-version>')
  process.exit(1)
}

const root = 'node_modules/minecraft-data/minecraft-data/data'
const paths = JSON.parse(readFileSync(join(root, 'dataPaths.json'), 'utf8'))

const entry = paths.bedrock?.[version]
if (!entry) {
  console.error(`no dataPaths entry for bedrock ${version} — refusing to prune blind`)
  process.exit(1)
}

const keep = new Set(Object.values(entry))
// Not referenced per-file, but read by the loader when resolving a version.
for (const shared of ['bedrock/common', 'pc/common']) {
  try {
    if (statSync(join(root, shared)).isDirectory()) keep.add(shared)
  } catch {
    // Absent in this release of the package; nothing to preserve.
  }
}

let removed = 0
for (const edition of ['bedrock', 'pc']) {
  let entries
  try {
    entries = readdirSync(join(root, edition))
  } catch {
    continue
  }
  for (const name of entries) {
    const relative = `${edition}/${name}`
    if (keep.has(relative)) continue
    const absolute = join(root, relative)
    if (!statSync(absolute).isDirectory()) continue
    rmSync(absolute, { recursive: true, force: true })
    removed += 1
  }
}

console.log(`kept ${keep.size} data dirs for bedrock ${version}, removed ${removed}`)
